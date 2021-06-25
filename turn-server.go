package main

import (
	"github.com/pion/logging"
	"github.com/pion/stun"
	"github.com/pion/turn"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func createAuthHandler(usersMap map[string]string) turn.AuthHandler {
	return func(username string, srcAddr net.Addr) (string, bool) {
		if password, ok := usersMap[username]; ok {
			return password, true
		}
		return "", false
	}
}

func main() {
	usersMap := map[string]string{}
	usersMap["jac"] = "jac"

	conf := &turn.ServerConfig{
		Realm:              "coolpy.net",
		AuthHandler:        createAuthHandler(usersMap),
		ChannelBindTimeout: 30 * time.Second,
		ListeningPort:      stun.DefaultPort,
		LoggerFactory:      logging.NewDefaultLoggerFactory(),
		Software:           "SOFTWARE",
	}

	srv := turn.NewServer(conf)

	err := srv.Start()
	if err != nil {
		log.Panic(err)
	}

	log.Println("turn server on port", conf.ListeningPort)

	signalChan := make(chan os.Signal, 1)
	cleanupDone := make(chan bool)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		for range signalChan {
			_ = srv.Close()
			log.Println("safe exit")
			cleanupDone <- true
		}
	}()
	<-cleanupDone
}
