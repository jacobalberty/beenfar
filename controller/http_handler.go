package controller

import (
	"io/ioutil"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jacobalberty/beenfar/util"
)

type HttpHandler struct {
	devices devices
}

type devices struct {
	Pending map[string]*pending `json:"pending,omitempty"`
}

type pending struct {
	Timestamp int64 `json:"-"`
}

func (h *HttpHandler) RegisterHandlers() {
	router := gin.Default()

	router.GET("/inform", h.informHandler)
	router.GET("/api/device/list", h.getDeviceList)

	router.Run()
}

func (h *HttpHandler) informHandler(c *gin.Context) {
	bodyBuffer, _ := ioutil.ReadAll(c.Request.Body)
	ipd, err := util.NewInformPD(bodyBuffer)
	if err != nil {
		// Do err
	}
	if _, ok := h.devices.Pending[ipd.Mac]; !ok {
		h.devices.Pending[ipd.Mac] = &pending{time.Now().Unix()}
	} else {
		h.devices.Pending[ipd.Mac].Timestamp = time.Now().Unix()

	}
}

func (h *HttpHandler) getDeviceList(c *gin.Context) {
	c.JSON(http.StatusOK, h.devices)
}
