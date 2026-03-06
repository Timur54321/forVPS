package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/multiformats/go-multiaddr"
)

var (
	stream1 network.Stream
	stream2 network.Stream
	mu      sync.Mutex
)

const keyFile = "relay.key"

type RegisteredFile struct {
	ID            string `json:"fileID"`
	Name          string `json:"filename"`
	Size          int64  `json:"size"`
	SizeFormatted string `json:"size_formatted"`
}

type RegisteredFileStream struct {
	FileID  string
	OwnerID string
	str     network.Stream
}

var registeredFiles []RegisteredFile
var registeredStreams []RegisteredFileStream

const RegisterFileProtocolID = "/register_file/1.0.0"
const FilesForSaleProtocolID = "/files_for_sale/1.0.0"
const FileWaitSignal = "/waitForSignalToTransmitFile/1.0.0"
const BuyFileProtocolID = "/buyFile/1.0.0"
const transmitProtocolID = "/transmitFile"

func sendFilesForSale(s network.Stream) {
	defer s.Close()

	encoder := json.NewEncoder(s)
	err := encoder.Encode(registeredFiles)
	if err != nil {
		log.Println(err)
	}

	fmt.Println("Files for sales successfully sent")
}

func registerFileSH(s network.Stream) {
	defer s.Close()

	var received RegisteredFile
	decoder := json.NewDecoder(s)

	err := decoder.Decode(&received)
	if err != nil {
		log.Printf("Error decoding file: %v", err)
		return
	}

	fmt.Printf("Received struct: %+v\n", received)
	s.Write([]byte("OK"))
	registeredFiles = append(registeredFiles, received)
}

func handleStream(s network.Stream) {
	// /transmitFile/fileid/owner/ownerid/1.1.0
	// IF protocol has owner id and file id,
	// THEN stream1 = corresponding stream
	// ELSE save remote peer, fileid and corresponding stream

	mu.Lock()
	defer mu.Unlock()

	log.Println("Got a new stream from:", s.Conn().RemotePeer())

	prot := string(s.Protocol())
	parts := strings.Split(prot, "/")

	if len(parts) > 4 {
		fileID := parts[2]
		ownerID := parts[4]

		if ownerID == "none" {
			var foundIndex int = -1
			for i, v := range registeredStreams {
				if v.OwnerID == s.Conn().ID() {
					foundIndex = i
					break
				}
			}

			if foundIndex == -1 {
				registeredStreams = append(registeredStreams, RegisteredFileStream{
					FileID:  fileID,
					OwnerID: s.Conn().ID(),
					str:     s,
				})
			} else {
				registeredStreams[foundIndex].str = s
			}

			log.Println("Stream1 registered")
		} else {
			for _, v := range registeredStreams {
				if v.FileID == fileID && v.OwnerID == ownerID {
					go bridgeStreams(v.str, s)
					break
				}
			}

			log.Println("Stream2 registered")
		}
	}

	// if stream1 == nil {
	// 	stream1 = s

	// 	return
	// }

	// if stream2 == nil {
	// 	stream2 = s
	// 	log.Println("Stream2 registered")

	// 	// когда оба подключены — запускаем туннель
	// 	go bridgeStreams(stream1, stream2)
	// 	return
	// }

	// // если вдруг пришёл третий — закрываем
	// log.Println("Already have 2 streams, closing extra")
	// s.Close()
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

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h, err := makeHost()
	if err != nil {
		log.Println(err)
		return
	}

	startPeer(ctx, h, handleStream)

	// Wait forever
	select {}
}

func makeHost() (host.Host, error) {
	priv := loadOrCreateKey()

	return libp2p.New(
		libp2p.ListenAddrStrings("/ip4/0.0.0.0/tcp/9000"),
		libp2p.Identity(priv),
	)
}

func startPeer(_ context.Context, h host.Host, streamHandler network.StreamHandler) {

	// h.SetStreamHandler(transmitProtocolID, streamHandler)
	h.SetStreamHandlerMatch(transmitProtocolID, func(prot protocol.ID) bool {
		return strings.HasPrefix(string(prot), transmitProtocolID)
	}, streamHandler)
	h.SetStreamHandler(RegisterFileProtocolID, registerFileSH)
	h.SetStreamHandler(FilesForSaleProtocolID, sendFilesForSale)

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

func loadOrCreateKey() crypto.PrivKey {
	if data, err := os.ReadFile(keyFile); err == nil {
		b, _ := hex.DecodeString(string(data))
		k, _ := crypto.UnmarshalPrivateKey(b)
		return k
	}

	k, _, _ := crypto.GenerateEd25519Key(rand.Reader)
	b, _ := crypto.MarshalPrivateKey(k)
	os.WriteFile(keyFile, []byte(hex.EncodeToString(b)), 0600)
	return k
}
