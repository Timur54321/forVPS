package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

const protocolID = "/file-transfer/1.0.0"

func main() {
	ctx := context.Background()

	h, err := libp2p.New()
	if err != nil {
		panic(err)
	}

	fmt.Println("Мой PeerID:", h.ID())
	for _, addr := range h.Addrs() {
		fmt.Printf("Мой адрес: %s/p2p/%s\n", addr, h.ID())
	}

	// Обработчик входящих stream
	h.SetStreamHandler(protocolID, func(s network.Stream) {
		go receiveFile(s)
	})

	// Если передали адрес peer — подключаемся
	if len(os.Args) > 1 {
		connectToPeer(ctx, h, os.Args[1])
	}

	select {}
}

func connectToPeer(ctx context.Context, h host.Host, addr string) {
	maddr, err := multiaddr.NewMultiaddr(addr)
	if err != nil {
		panic(err)
	}

	info, err := peer.AddrInfoFromP2pAddr(maddr)
	if err != nil {
		panic(err)
	}

	if err := h.Connect(ctx, *info); err != nil {
		panic(err)
	}

	fmt.Println("Успешно подключено к peer")

	go senderLoop(ctx, h, info.ID)
}

func senderLoop(ctx context.Context, h host.Host, peerID peer.ID) {
	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Print("Введите путь к файлу > ")
		path, _ := reader.ReadString('\n')
		path = strings.TrimSpace(path)

		file, err := os.Open(path)
		if err != nil {
			fmt.Println("Ошибка:", err)
			continue
		}

		stream, err := h.NewStream(ctx, peerID, protocolID)
		if err != nil {
			fmt.Println("Ошибка открытия stream:", err)
			file.Close()
			continue
		}

		filename := filepath.Base(path)

		// отправляем имя файла
		_, err = stream.Write([]byte(filename + "\n"))
		if err != nil {
			fmt.Println("Ошибка отправки имени файла:", err)
			file.Close()
			stream.Close()
			continue
		}

		// отправляем содержимое
		_, err = io.Copy(stream, file)
		if err != nil {
			fmt.Println("Ошибка отправки файла:", err)
		}

		fmt.Println("Файл отправлен:", filename)

		file.Close()
		stream.Close()
	}
}

func receiveFile(stream network.Stream) {
	defer stream.Close()

	reader := bufio.NewReader(stream)

	// читаем имя файла
	filename, err := reader.ReadString('\n')
	if err != nil {
		fmt.Println("Ошибка чтения имени файла:", err)
		return
	}
	filename = strings.TrimSpace(filename)

	out, err := os.Create("received_" + filename)
	if err != nil {
		fmt.Println("Ошибка создания файла:", err)
		return
	}
	defer out.Close()

	_, err = io.Copy(out, reader)
	if err != nil {
		fmt.Println("Ошибка при получении файла:", err)
		return
	}

	fmt.Println("Файл получен:", filename)
}
