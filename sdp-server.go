package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"yiyilive/src/sdp"
)

func main() {
	port := flag.String("p", ":8443", "https port")
	flag.Parse()

	dir, err := filepath.Abs(filepath.Dir(os.Args[0]))
	if err != nil {
		log.Println(err)
		return
	}

	hub := sdp.NewHub()
	go hub.Run()

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		sdp.ServeWs(hub, w, r)
	})
	go func() {
		if err := http.ListenAndServeTLS(*port, dir+"/sdp.icoolpy.com.pem", dir+"/sdp.icoolpy.com.key", nil); err != nil {
			log.Panic("stopped", err)
		}
	}()
	log.Println("sdp listening", *port)

	signalChan := make(chan os.Signal, 1)
	cleanupDone := make(chan bool)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		for range signalChan {
			log.Println("safe exit")
			cleanupDone <- true
		}
	}()
	<-cleanupDone
}
