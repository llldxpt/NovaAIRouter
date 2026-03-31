package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"
)

const (
	BroadcastPort = 15053
	ReplyPort    = 15054
)

type BroadcastMessage struct {
	Type   string `json:"type"`
	NodeID string `json:"node_id"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: udp_test <sender|receiver>")
		os.Exit(1)
	}

	mode := os.Args[1]

	if mode == "sender" {
		sender()
	} else if mode == "receiver" {
		receiver()
	} else {
		fmt.Println("Unknown mode:", mode)
		os.Exit(1)
	}
}

func sender() {
	fmt.Println("[Sender] Starting UDP broadcast sender...")

	broadcastAddr := fmt.Sprintf("255.255.255.255:%d", BroadcastPort)
	udpAddr, err := net.ResolveUDPAddr("udp4", broadcastAddr)
	if err != nil {
		fmt.Printf("[Sender] Failed to resolve address: %v\n", err)
		os.Exit(1)
	}

	conn, err := net.DialUDP("udp4", nil, udpAddr)
	if err != nil {
		fmt.Printf("[Sender] Failed to create UDP connection: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	// 设置 SO_BROADCAST
	fd, err := conn.SyscallConn()
	if err == nil {
		fd.Control(func(fd uintptr) {
			syscall.SetsockoptInt(syscall.Handle(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
		})
	}

	// 监听确认回复
	replyAddr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf(":%d", ReplyPort))
	if err != nil {
		fmt.Printf("[Sender] Failed to resolve reply address: %v\n", err)
		os.Exit(1)
	}

	replyConn, err := net.ListenUDP("udp4", replyAddr)
	if err != nil {
		fmt.Printf("[Sender] Failed to listen on reply port: %v\n", err)
		os.Exit(1)
	}
	defer replyConn.Close()

	replyConn.SetReadDeadline(time.Now().Add(10 * time.Second))

	fmt.Printf("[Sender] Broadcasting on %s (reply port %d)\n", broadcastAddr, ReplyPort)

	// 发送广播
	for i := 0; i < 50; i++ {
		msg := BroadcastMessage{
			Type:   "announce",
			NodeID: "sender-node",
		}
		data, _ := json.Marshal(msg)

		n, err := conn.Write(data)
		if err != nil {
			fmt.Printf("[Sender] Write error: %v\n", err)
			return
		}
		fmt.Printf("[Sender] Sent broadcast #%d (%d bytes)\n", i+1, n)

		// 等待确认
		buf := make([]byte, 1024)
		replyConn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, replyFrom, err := replyConn.ReadFromUDP(buf)
		if err == nil {
			fmt.Printf("[Sender] Got reply from %s: %s\n", replyFrom.String(), string(buf[:n]))
			fmt.Println("[Sender] SUCCESS! Got reply, stopping broadcast!")
			return
		}

		time.Sleep(200 * time.Millisecond)
	}

	fmt.Println("[Sender] No reply received after 50 attempts, giving up")
}

func receiver() {
	fmt.Println("[Receiver] Starting UDP broadcast receiver...")

	addr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf(":%d", BroadcastPort))
	if err != nil {
		fmt.Printf("[Receiver] Failed to resolve address: %v\n", err)
		os.Exit(1)
	}

	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		fmt.Printf("[Receiver] Failed to listen on UDP: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	fmt.Printf("[Receiver] Listening on :%d\n", BroadcastPort)

	buf := make([]byte, 1024)
	for {
		conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		n, remoteAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				fmt.Println("[Receiver] Timeout, exiting")
				break
			}
			fmt.Printf("[Receiver] Read error: %v\n", err)
			continue
		}

		fmt.Printf("[Receiver] Received %d bytes from %v\n", n, remoteAddr.String())

		var msg BroadcastMessage
		if err := json.Unmarshal(buf[:n], &msg); err != nil {
			fmt.Printf("[Receiver] Failed to parse JSON: %v\n", err)
			continue
		}

		fmt.Printf("[Receiver] Parsed: Type=%s, NodeID=%s\n", msg.Type, msg.NodeID)

		// 直接使用 remoteAddr 的IP和ReplyPort发送确认
		replyAddr := fmt.Sprintf("%s:%d", remoteAddr.IP.String(), ReplyPort)
		replyUDPAddr, err := net.ResolveUDPAddr("udp4", replyAddr)
		if err != nil {
			fmt.Printf("[Receiver] Failed to resolve reply addr: %v\n", err)
			continue
		}

		replyConn, err := net.DialUDP("udp4", nil, replyUDPAddr)
		if err != nil {
			fmt.Printf("[Receiver] Failed to create reply connection: %v\n", err)
			continue
		}

		replyData := []byte("ACK from receiver")
		_, err = replyConn.Write(replyData)
		replyConn.Close()
		if err != nil {
			fmt.Printf("[Receiver] Failed to send reply: %v\n", err)
			continue
		}

		fmt.Printf("[Receiver] Sent ACK to %s\n", replyAddr)
	}
}
