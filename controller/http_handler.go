package controller

import (
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"time"
)

type HttpHandler struct{}

func (h *HttpHandler) RegisterHandlers() {
	http.HandleFunc("/inform", h.InformHandler)
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func (h *HttpHandler) InformHandler(w http.ResponseWriter, r *http.Request) {
	bodyBuffer, _ := ioutil.ReadAll(r.Body)
	fn := fmt.Sprintf("tmp/%d", time.Now().Unix())
	os.WriteFile(fn, bodyBuffer, 0644)
	log.Printf("Wrote %d bytes to %s\n", len(bodyBuffer), fn)
}
