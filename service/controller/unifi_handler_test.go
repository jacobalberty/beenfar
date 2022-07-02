package controller_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/jsonapi"
	"github.com/jacobalberty/beenfar/service/adapter/unifi"
	"github.com/jacobalberty/beenfar/service/controller"
	"github.com/jacobalberty/beenfar/service/model"
)

func TestUnifiPending(t *testing.T) {
	var (
		err      error
		ipd      unifi.InformPD
		ib       unifi.InformBuilder
		b        []byte
		h        *controller.HttpHandler
		req      *http.Request
		response *httptest.ResponseRecorder
		devices  model.Devices
	)

	t.Parallel()

	h = new(controller.HttpHandler)
	h.RegisterHandlers()

	// Check to make sure no devices are pending or adopted
	if req, err = http.NewRequest("GET", "/api/device", nil); err != nil {
		t.Fatal(err)
	}

	response = executeRequest(h, req)
	if response.Code != http.StatusOK {
		t.Errorf("Expected status code %v, got %v", http.StatusOK, response.Code)
	}

	if err = jsonapi.UnmarshalPayload(response.Body, &devices); err != nil {
		t.Fatal(err)
	}

	if len(devices.Pending) != 0 {
		t.Errorf("Expected 0 pending devices, got %v", len(devices.Pending))
	}
	if len(devices.Adopted) != 0 {
		t.Errorf("Expected 0 adopted devices, got %v", len(devices.Adopted))
	}

	// Create a unifi inform packet
	ipdBase := struct {
		Mac string `json:"mac"`
	}{
		Mac: "de:ad:be:ef:00:00",
	}

	ipd.Magic = 1414414933
	ipd.Version = 1
	ipd.Mac = "deadbeef0000"
	ipd.Flags = 0b1001
	ipd.DataVersion = 0

	ib.Init(ipd)

	if b, err = ib.BuildResponse(ipdBase); err != nil {
		t.Error(err)
	}

	// Send the packet to /inform
	if req, err = http.NewRequest("POST", "/inform", bytes.NewBuffer(b)); err != nil {
		t.Error(err)
	}

	response = executeRequest(h, req)

	// Not adopted yet so we get a 404
	if response.Code != http.StatusNotFound {
		t.Errorf("Expected status code %v, got %v", http.StatusOK, response.Code)
	}

	// Check for 0 Adopted and 1 Pending devices
	if req, err = http.NewRequest("GET", "/api/device", nil); err != nil {
		t.Error(err)
	}

	response = executeRequest(h, req)
	if response.Code != http.StatusOK {
		t.Errorf("Expected status code %v, got %v", http.StatusOK, response.Code)
	}

	if err = jsonapi.UnmarshalPayload(response.Body, &devices); err != nil {
		t.Error(err)
	}

	if len(devices.Pending) != 1 {
		t.Errorf("Expected 1 pending devices, got %v", len(devices.Pending))
	}

	if len(devices.Adopted) != 0 {
		t.Errorf("Expected 0 adopted devices, got %v", len(devices.Adopted))
	}

	// Adopt the device
	if req, err = http.NewRequest("POST", "/api/device/adopt/deadbeef0000", nil); err != nil {
		t.Error(err)
	}

	response = executeRequest(h, req)
	if response.Code != http.StatusOK {
		t.Errorf("Expected status code %v, got %v", http.StatusOK, response.Code)
	}

	// Check for 1 Adopted and 0 Pending devices
	if req, err = http.NewRequest("GET", "/api/device", nil); err != nil {
		t.Error(err)
	}

	response = executeRequest(h, req)
	if response.Code != http.StatusOK {
		t.Errorf("Expected status code %v, got %v", http.StatusOK, response.Code)
	}

	if err = jsonapi.UnmarshalPayload(response.Body, &devices); err != nil {
		t.Error(err)
	}

	if len(devices.Pending) != 0 {
		t.Errorf("Expected 0 pending devices, got %v", len(devices.Pending))
	}

	if len(devices.Adopted) != 1 {
		t.Errorf("Expected 1 adopted devices, got %v", len(devices.Adopted))
	}

	// Try Forgetting a device
	if req, err = http.NewRequest("DELETE", "/api/device/deadbeef0000", nil); err != nil {
		t.Error(err)
	}

	response = executeRequest(h, req)
	if response.Code != http.StatusNoContent {
		t.Errorf("Expected status code %v, got %v", http.StatusOK, response.Code)
	}

	// Check for 0 Adopted and 0 Pending devices
	if req, err = http.NewRequest("GET", "/api/device", nil); err != nil {
		t.Error(err)
	}

	response = executeRequest(h, req)
	if response.Code != http.StatusOK {
		t.Errorf("Expected status code %v, got %v", http.StatusOK, response.Code)
	}

	if err = jsonapi.UnmarshalPayload(response.Body, &devices); err != nil {
		t.Error(err)
	}

	if len(devices.Pending) != 0 {
		t.Errorf("Expected 0 pending devices, got %v", len(devices.Pending))
	}

	if len(devices.Adopted) != 0 {
		t.Errorf("Expected 0 adopted devices, got %v", len(devices.Adopted))
	}
}
