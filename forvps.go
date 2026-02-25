package main

import (
	"fmt"
	"io"
	"os"

	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/protocol"
)

const ProtocolID = protocol.ID("/file-transfer/1.0.0")

func main() {
	host, err := libp2p.New()
	if err != nil {
		panic(err)
	}

	fmt.Println("Receiver started")
	fmt.Println("Peer ID:", host.ID())
	for _, addr := range host.Addrs() {
		fmt.Printf("Address: %s/p2p/%s\n", addr, host.ID())
	}

	host.SetStreamHandler(ProtocolID, handleStream)

	select {}
}

func handleStream(s network.Stream) {
	defer s.Close()

	fmt.Println("Incoming file...")

	file, err := os.Create("received_file")
	if err != nil {
		panic(err)
	}
	defer file.Close()

	_, err = io.Copy(file, s)
	if err != nil {
		fmt.Println("Error receiving file:", err)
		return
	}

	fmt.Println("File received successfully")
}
