package main

import (
	"flag"
	"fmt"
	"net/http"
	"strings"
	"time"
)

var (
	port    = flag.Int("port", 18001, "Port to listen on")
	delay   = flag.Int("delay", 3, "Delay in seconds for all endpoints")
)

func main() {
	flag.Parse()

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		fmt.Printf("[%s] Received %s %s, sleeping %d seconds...\n", time.Now().Format("15:04:05"), r.Method, path, *delay)
		time.Sleep(time.Duration(*delay) * time.Second)
		fmt.Printf("[%s] Sending response for %s...\n", time.Now().Format("15:04:05"), path)

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-Delay", fmt.Sprintf("%d", *delay))

		if strings.Contains(path, "chat/completions") {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"id":"chatcmpl-123","object":"chat.completion","created":1234567890,"model":"gpt-3.5-turbo","choices":[{"index":0,"message":{"role":"assistant","content":"Hello! This is a test response."},"finish_reason":"stop"}]}`))
		} else if strings.Contains(path, "queue") {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"id":"queue-123","object":"queue.response","created":1234567890,"status":"completed"}`))
		} else if strings.Contains(path, "models") {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"object":"list","data":[{"id":"gpt-3.5-turbo","object":"model","created":1677610602,"owned_by":"openai"}]}`))
		} else {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"ok","path":"` + path + `"}`))
		}
	})

	fmt.Printf("Mock backend server starting on :%d\n", *port)
	fmt.Printf("All endpoints will sleep %d seconds\n", *delay)
	if err := http.ListenAndServe(fmt.Sprintf(":%d", *port), nil); err != nil {
		fmt.Println("Error:", err)
	}
}
