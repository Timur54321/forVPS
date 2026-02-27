/*
 *
 * The MIT License (MIT)
 *
 * Copyright (c) 2014 Juan Batiz-Benet
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in
 * all copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
 * THE SOFTWARE.
 *
 * This program demonstrate a simple chat application using p2p communication.
 *
 */
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"io"
	"log"
	mrand "math/rand"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"

	"github.com/multiformats/go-multiaddr"
)

var (
	stream1 network.Stream
	stream2 network.Stream
	mu      sync.Mutex
)

func handleStream(s network.Stream) {
	log.Println("Got a new stream from:", s.Conn().RemotePeer())

	mu.Lock()
	defer mu.Unlock()

	if stream1 == nil {
		stream1 = s
		log.Println("Stream1 registered")
		return
	}

	if stream2 == nil {
		stream2 = s
		log.Println("Stream2 registered")

		// когда оба подключены — запускаем туннель
		go bridgeStreams(stream1, stream2)
		return
	}

	// если вдруг пришёл третий — закрываем
	log.Println("Already have 2 streams, closing extra")
	s.Close()
}

func bridgeStreams(a, b network.Stream) {
	log.Println("Starting bidirectional bridge")

	var wg sync.WaitGroup
	wg.Add(2)

	// A → B
	go func() {
		defer wg.Done()
		io.Copy(b, a)
	}()

	// B → A
	go func() {
		defer wg.Done()
		io.Copy(a, b)
	}()

	wg.Wait()

	log.Println("Bridge finished, closing streams")

	a.Close()
	b.Close()

	mu.Lock()
	stream1 = nil
	stream2 = nil
	mu.Unlock()
}

func readData(s network.Stream) {
	reader := bufio.NewReader(s)

	for {
		// читаем имя файла
		filename, err := reader.ReadString('\n')
		if err != nil {
			fmt.Println("Ошибка чтения имени:", err)
			return
		}
		filename = strings.TrimSpace(filename)

		// читаем размер
		sizeStr, err := reader.ReadString('\n')
		if err != nil {
			fmt.Println("Ошибка чтения размера:", err)
			return
		}
		sizeStr = strings.TrimSpace(sizeStr)

		filesize, err := strconv.ParseInt(sizeStr, 10, 64)
		if err != nil {
			fmt.Println("Ошибка парсинга размера:", err)
			return
		}

		out, err := os.Create("received_" + filename)
		if err != nil {
			fmt.Println("Ошибка создания файла:", err)
			return
		}

		// читаем ровно filesize байт
		_, err = io.CopyN(out, reader, filesize)
		if err != nil {
			fmt.Println("Ошибка при получении файла:", err)
			out.Close()
			return
		}

		out.Close()

		fmt.Println("\nФайл получен:", filename)
		fmt.Print("Введите путь к файлу > ")
	}
}

func writeData(s network.Stream) {
	stdReader := bufio.NewReader(os.Stdin)
	writer := bufio.NewWriter(s)

	for {
		fmt.Print("Введите путь к файлу > ")

		path, err := stdReader.ReadString('\n')
		if err != nil {
			fmt.Println("Ошибка ввода:", err)
			return
		}

		path = strings.TrimSpace(path)

		file, err := os.Open(path)
		if err != nil {
			fmt.Println("Ошибка открытия файла:", err)
			continue
		}

		info, err := file.Stat()
		if err != nil {
			fmt.Println("Ошибка получения размера:", err)
			file.Close()
			continue
		}

		filename := filepath.Base(path)
		filesize := info.Size()

		// отправляем имя
		writer.WriteString(filename + "\n")

		// отправляем размер
		writer.WriteString(fmt.Sprintf("%d\n", filesize))

		writer.Flush()

		// отправляем содержимое
		_, err = io.CopyN(writer, file, filesize)
		if err != nil {
			fmt.Println("Ошибка отправки файла:", err)
			file.Close()
			return
		}

		writer.Flush()
		file.Close()

		fmt.Println("Файл отправлен:", filename)
	}
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sourcePort := flag.Int("sp", 0, "Source port number")
	dest := flag.String("d", "", "Destination multiaddr string")
	help := flag.Bool("help", false, "Display help")
	debug := flag.Bool("debug", false, "Debug generates the same node ID on every execution")

	flag.Parse()

	if *help {
		fmt.Printf("This program demonstrates a simple p2p chat application using libp2p\n\n")
		fmt.Println("Usage: Run './chat -sp <SOURCE_PORT>' where <SOURCE_PORT> can be any port number.")
		fmt.Println("Now run './chat -d <MULTIADDR>' where <MULTIADDR> is multiaddress of previous listener host.")

		os.Exit(0)
	}

	// If debug is enabled, use a constant random source to generate the peer ID. Only useful for debugging,
	// off by default. Otherwise, it uses rand.Reader.
	var r io.Reader
	if *debug {
		// Use the port number as the randomness source.
		// This will always generate the same host ID on multiple executions, if the same port number is used.
		// Never do this in production code.
		r = mrand.New(mrand.NewSource(int64(*sourcePort)))
	} else {
		r = rand.Reader
	}

	h, err := makeHost(*sourcePort, r)
	if err != nil {
		log.Println(err)
		return
	}

	if *dest == "" {
		startPeer(ctx, h, handleStream)
	} else {
		s, err := startPeerAndConnect(ctx, h, *dest)
		if err != nil {
			log.Println(err)
			return
		}

		// Create a thread to read and write data.
		go writeData(s)
		go readData(s)

	}

	// Wait forever
	select {}
}

func getLocalIP() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}

	for _, iface := range ifaces {
		// пропускаем виртуальные и выключенные
		if iface.Flags&net.FlagUp == 0 ||
			iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}

			ip := ipNet.IP

			// только IPv4
			if ip.To4() == nil {
				continue
			}

			s := ip.String()

			// ❗ исключаем docker диапазоны
			if strings.HasPrefix(s, "172.17.") ||
				strings.HasPrefix(s, "172.18.") ||
				strings.HasPrefix(s, "172.19.") {
				continue
			}

			return s
		}
	}

	return ""
}

func makeHost(port int, randomness io.Reader) (host.Host, error) {
	// Creates a new RSA key pair for this host.
	prvKey, _, err := crypto.GenerateKeyPairWithReader(crypto.RSA, 2048, randomness)
	if err != nil {
		log.Println(err)
		return nil, err
	}

	// 0.0.0.0 will listen on any interface device.
	ip := getLocalIP()
	sourceMultiAddr, _ := multiaddr.NewMultiaddr(fmt.Sprintf("/ip4/%s/tcp/%d", ip, port))
	fmt.Println(ip)

	// libp2p.New constructs a new libp2p Host.
	// Other options can be added here.
	return libp2p.New(
		libp2p.ListenAddrs(sourceMultiAddr),
		libp2p.Identity(prvKey),
	)
}

func startPeer(_ context.Context, h host.Host, streamHandler network.StreamHandler) {
	// Set a function as stream handler.
	// This function is called when a peer connects, and starts a stream with this protocol.
	// Only applies on the receiving side.
	h.SetStreamHandler("/chat/1.0.0", streamHandler)
	fmt.Println(h.Network())

	// Let's get the actual TCP port from our listen multiaddr, in case we're using 0 (default; random available port).
	var port string
	for _, la := range h.Network().ListenAddresses() {
		if p, err := la.ValueForProtocol(multiaddr.P_TCP); err == nil {
			port = p
			break
		}
	}

	if port == "" {
		log.Println("was not able to find actual local port")
		return
	}

	log.Printf("Run './chat -d /ip4/127.0.0.1/tcp/%v/p2p/%s' on another console.\n", port, h.ID())
	log.Println("You can replace 127.0.0.1 with public IP as well.")
	log.Println("Waiting for incoming connection")
	log.Println()
}

func startPeerAndConnect(_ context.Context, h host.Host, destination string) (network.Stream, error) {
	log.Println("This node's multiaddresses:")
	for _, la := range h.Addrs() {
		log.Printf(" - %v\n", la)
	}
	log.Println()

	// Turn the destination into a multiaddr.
	maddr, err := multiaddr.NewMultiaddr(destination)
	if err != nil {
		log.Println(err)
		return nil, err
	}

	// Extract the peer ID from the multiaddr.
	info, err := peer.AddrInfoFromP2pAddr(maddr)
	if err != nil {
		log.Println(err)
		return nil, err
	}

	// Add the destination's peer multiaddress in the peerstore.
	// This will be used during connection and stream creation by libp2p.
	h.Peerstore().AddAddrs(info.ID, info.Addrs, peerstore.PermanentAddrTTL)

	// Start a stream with the destination.
	// Multiaddress of the destination peer is fetched from the peerstore using 'peerId'.
	s, err := h.NewStream(context.Background(), info.ID, "/chat/1.0.0")
	if err != nil {
		log.Println(err)
		return nil, err
	}
	log.Println("Established connection to destination")

	return s, nil
}
