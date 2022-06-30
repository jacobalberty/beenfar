package controller

import (
	"log"
	"net/http"

	"github.com/google/jsonapi"
	"github.com/jacobalberty/beenfar/service/model"
	"github.com/julienschmidt/httprouter"
)

type HttpHandler struct {
	devices      *model.Devices
	wifiNetworks map[string]model.WifiNetworkConfig
}

func (h *HttpHandler) RegisterHandlers() {
	h.wifiNetworks = make(map[string]model.WifiNetworkConfig)
	h.devices = new(model.Devices)
	h.devices.Init()

	router := httprouter.New()

	unifi := unifiHandler{}
	unifi.Init(router, h.devices)

	// Unstable apis
	router.POST("/api/device/adopt/:mac", h.postDeviceAdopt)
	router.GET("/api/device/list", h.getDeviceList)
	router.GET("/api/wifi/list", h.getWifiList)
	router.GET("/api/wifi/:ssid", h.getWifiBySSID)
	router.POST("/api/wifi", h.postWifi)
	router.PUT("/api/wifi", h.putWifi)
	router.DELETE("/api/wifi/:ssid", h.deleteWifi)

	log.Fatal(http.ListenAndServe(":8080", router))
}

// Gets a list of all devices
func (h *HttpHandler) getDeviceList(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	w.Header().Set("Content-Type", jsonapi.MediaType)
	w.WriteHeader(http.StatusOK)
	if err := jsonapi.MarshalPayload(w, &h.devices); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}

}

// Adopts a device by MAC address
func (h *HttpHandler) postDeviceAdopt(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	w.Header().Set("Content-Type", jsonapi.MediaType)
	w.WriteHeader(http.StatusNotImplemented)
	if err := jsonapi.MarshalErrors(w, []*jsonapi.ErrorObject{{
		Title:  "Device Adoption",
		Detail: "Device adoption is not yet implemented",
		Status: "501",
	}}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// Creates a new wifi network using model.WifiNetworkConfig
func (h *HttpHandler) postWifi(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	w.Header().Set("Content-Type", jsonapi.MediaType)

	WifiNetwork := new(model.WifiNetworkConfig)
	if err := jsonapi.UnmarshalPayload(r.Body, WifiNetwork); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}

	if _, ok := h.wifiNetworks[WifiNetwork.Ssid]; ok {
		if err := jsonapi.MarshalErrors(w, []*jsonapi.ErrorObject{{
			Title:  "Wifi Network Already Exists",
			Detail: "Wifi network with SSID " + WifiNetwork.Ssid + " already exists",
			Status: "409",
		}}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
	w.WriteHeader(http.StatusCreated)
	h.wifiNetworks[WifiNetwork.Ssid] = *WifiNetwork
}

// Update existing wifi network using model.WifiNetworkConfig
func (h *HttpHandler) putWifi(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	w.Header().Set("Content-Type", jsonapi.MediaType)

	WifiNetwork := new(model.WifiNetworkConfig)
	if err := jsonapi.UnmarshalPayload(r.Body, WifiNetwork); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}

	if _, ok := h.wifiNetworks[WifiNetwork.Ssid]; !ok {
		if err := jsonapi.MarshalErrors(w, []*jsonapi.ErrorObject{{
			Title:  "Wifi Network Not Found",
			Detail: "Wifi network with SSID " + WifiNetwork.Ssid + " does not exist",
			Status: "404",
		}}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
	w.WriteHeader(http.StatusOK)
	h.wifiNetworks[WifiNetwork.Ssid] = *WifiNetwork
}

// deletes a wifi network by SSID
func (h *HttpHandler) deleteWifi(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	w.Header().Set("Content-Type", jsonapi.MediaType)

	ssid := params.ByName("ssid")
	if _, ok := h.wifiNetworks[ssid]; !ok {
		if err := jsonapi.MarshalErrors(w, []*jsonapi.ErrorObject{{
			Title:  "Wifi Network Not Found",
			Detail: "Wifi network with SSID " + ssid + " does not exist",
			Status: "404",
		}}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
	delete(h.wifiNetworks, ssid)
	w.WriteHeader(http.StatusNoContent)
}

// Returns a list of all wifi networks
func (h *HttpHandler) getWifiList(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	w.Header().Set("Content-Type", jsonapi.MediaType)
	w.WriteHeader(http.StatusOK)
	if err := jsonapi.MarshalPayload(w, h.wifiNetworks); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// Returns a wifi network with the given SSID
func (h *HttpHandler) getWifiBySSID(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	w.Header().Set("Content-Type", jsonapi.MediaType)

	ssid := params.ByName("ssid")
	if _, ok := h.wifiNetworks[ssid]; !ok {
		if err := jsonapi.MarshalErrors(w, []*jsonapi.ErrorObject{{
			Title:  "Wifi Network Not Found",
			Detail: "Wifi network with SSID " + ssid + " does not exist",
			Status: "404",
		}}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
	w.WriteHeader(http.StatusOK)
	if err := jsonapi.MarshalPayload(w, h.wifiNetworks[ssid]); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
