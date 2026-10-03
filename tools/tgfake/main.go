// Command tgfake serves a fake Telegram Bot API (see internal/tgfake) for
// local development and the CI end-to-end install run. Point the bot at it
// with BOBRES_TELEGRAM_API_URL=http://<host>:<port>.
package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tgfake"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8081", "listen address")
	flag.Parse()
	srv := &http.Server{
		Addr:              *addr,
		Handler:           tgfake.New().Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("fake Telegram Bot API on %s (POST /_inject, GET /_sent)", *addr)
	log.Fatal(srv.ListenAndServe())
}
