package controller

import (
	"bytes"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/jsonapi"
	"github.com/jacobalberty/beenfar/service/model"
)

type HttpHandler struct {
	devices      *model.Devices
	wifiNetworks map[string]model.WifiNetworkConfig
	mux          *chi.Mux
}

func (h *HttpHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *HttpHandler) RegisterHandlers() {
	h.wifiNetworks = make(map[string]model.WifiNetworkConfig)
	h.devices = new(model.Devices)
	h.devices.Init()

	h.mux = chi.NewRouter()

	unifi := unifiHandler{}
	unifi.Init(h.mux, h.devices)

	// Unstable apis
	h.mux.Post("/api/device/adopt/{mac:^([[:xdigit:]]{2}[:-]?){6}$}", h.PostDeviceAdopt)
	h.mux.Delete("/api/device/{mac:^([[:xdigit:]]{2}[:-]?){6}$}", h.DeleteDevice)
	h.mux.Get("/api/device", h.GetDeviceList)
	h.mux.Get("/api/wifi", h.GetWifiList)
	h.mux.Get("/api/wifi/{ssid:^[[:alnum:] ]+$}", h.GetWifiBySSID)
	h.mux.Post("/api/wifi", h.PostWifi)
	h.mux.Put("/api/wifi/{ssid:^[[:alnum:] ]+$}", h.PutWifi)
	h.mux.Delete("/api/wifi/{ssid:^[[:alnum:] ]+$}", h.DeleteWifi)

}

// Gets a list of all devices
func (h *HttpHandler) GetDeviceList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", jsonapi.MediaType)
	if err := jsonapi.MarshalPayload(w, &h.devices); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}

}

// Adopts a device by MAC address
func (h *HttpHandler) PostDeviceAdopt(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", jsonapi.MediaType)

	mac := chi.URLParam(r, "mac")
	if err := h.devices.Adopt(mac); err != nil {
		if err2 := jsonapi.MarshalErrors(w, []*jsonapi.ErrorObject{{
			Title:  "Error adopting device",
			Detail: err.Error(),
			Status: "500",
		}}); err2 != nil {
			http.Error(w, err2.Error(), http.StatusInternalServerError)
		}
		return
	}
	w.WriteHeader(http.StatusOK)
}

// Forgets a network device by MAC address
func (h *HttpHandler) DeleteDevice(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", jsonapi.MediaType)

	mac := chi.URLParam(r, "mac")
	if err := h.devices.Delete(mac); err != nil {
		if err2 := jsonapi.MarshalErrors(w, []*jsonapi.ErrorObject{{
			Title:  "Error forgetting device",
			Detail: err.Error(),
			Status: "500",
		}}); err2 != nil {
			http.Error(w, err2.Error(), http.StatusInternalServerError)
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Creates a new wifi network using model.WifiNetworkConfig
func (h *HttpHandler) PostWifi(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", jsonapi.MediaType)

	WifiNetwork := new(model.WifiNetworkConfig)
	if err := jsonapi.UnmarshalPayload(r.Body, WifiNetwork); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
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
func (h *HttpHandler) PutWifi(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", jsonapi.MediaType)

	ssid := chi.URLParam(r, "ssid")
	WifiNetwork := new(model.WifiNetworkConfig)
	if err := jsonapi.UnmarshalPayload(r.Body, WifiNetwork); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if _, ok := h.wifiNetworks[ssid]; !ok {
		if err := jsonapi.MarshalErrors(w, []*jsonapi.ErrorObject{{
			Title:  "Wifi Network Not Found",
			Detail: "Wifi network with SSID " + WifiNetwork.Ssid + " does not exist",
			Status: "404",
		}}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	w.WriteHeader(http.StatusOK)
	if WifiNetwork.Ssid != ssid {
		delete(h.wifiNetworks, ssid)
	}
	h.wifiNetworks[WifiNetwork.Ssid] = *WifiNetwork
}

// deletes a wifi network by SSID
func (h *HttpHandler) DeleteWifi(w http.ResponseWriter, r *http.Request) {
	ssid := chi.URLParam(r, "ssid")
	if _, ok := h.wifiNetworks[ssid]; !ok {
		w.Header().Set("Content-Type", jsonapi.MediaType)
		if err := jsonapi.MarshalErrors(w, []*jsonapi.ErrorObject{{
			Title:  "Wifi Network Not Found",
			Detail: "Wifi network with SSID " + ssid + " does not exist",
			Status: "404",
		}}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	w.WriteHeader(http.StatusNoContent)
	delete(h.wifiNetworks, ssid)
}

// Returns a list of all wifi networks
func (h *HttpHandler) GetWifiList(w http.ResponseWriter, r *http.Request) {
	var (
		networkList []*model.WifiNetworkConfig
	)
	networkList = make([]*model.WifiNetworkConfig, 0, len(h.wifiNetworks))
	for _, network := range h.wifiNetworks {
		network := network
		networkList = append(networkList, &network)
	}

	w.Header().Set("Content-Type", jsonapi.MediaType)
	if err := jsonapi.MarshalPayload(w, networkList); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// Returns a wifi network with the given SSID
func (h *HttpHandler) GetWifiBySSID(w http.ResponseWriter, r *http.Request) {
	var (
		buf bytes.Buffer
	)
	w.Header().Set("Content-Type", jsonapi.MediaType)

	ssid := chi.URLParam(r, "ssid")
	if _, ok := h.wifiNetworks[ssid]; !ok {
		if err := jsonapi.MarshalErrors(&buf, []*jsonapi.ErrorObject{{
			Title:  "Wifi Network Not Found",
			Detail: "Wifi network with SSID " + ssid + " does not exist",
			Status: "404",
		}}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, err := w.Write(buf.Bytes())
		if err != nil {
			log.Println(err.Error())
		}
		return
	}
	network := h.wifiNetworks[ssid]
	if err := jsonapi.MarshalPayloadWithoutIncluded(w, &network); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
