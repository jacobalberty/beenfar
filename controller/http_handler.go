package controller

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"time"

	"github.com/gin-gonic/gin"
)

type HttpHandler struct{}

func (h *HttpHandler) RegisterHandlers() {
	router := gin.Default()

	router.GET("/inform", h.InformHandler)

	router.Run()
}

func (h *HttpHandler) InformHandler(c *gin.Context) {
	bodyBuffer, _ := ioutil.ReadAll(c.Request.Body)
	fn := fmt.Sprintf("tmp/%d", time.Now().Unix())
	os.WriteFile(fn, bodyBuffer, 0644)
	log.Printf("Wrote %d bytes to %s\n", len(bodyBuffer), fn)
}
