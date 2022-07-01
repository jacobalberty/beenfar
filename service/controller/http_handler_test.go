package controller_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/jsonapi"
	"github.com/jacobalberty/beenfar/service/controller"
	"github.com/jacobalberty/beenfar/service/model"
)

func TestWifiNetwork(t *testing.T) {
	var (
		h        *controller.HttpHandler
		bTmp     bytes.Buffer
		response *httptest.ResponseRecorder
	)
	h = new(controller.HttpHandler)
	h.RegisterHandlers()

	// With a non-existent SSID
	req, err := http.NewRequest("GET", "/api/wifi/test", nil)
	if err != nil {
		t.Fatal(err)
	}

	response = executeRequest(h, req)
	if response.Code != http.StatusNotFound {
		t.Errorf("Expected status %d, got %d", http.StatusNotFound, response.Code)
	}

	testWifi := model.WifiNetworkConfig{
		Ssid:        "test",
		SecurityKey: "key",
	}

	err = jsonapi.MarshalPayload(&bTmp, &testWifi)
	if err != nil {
		t.Fatal(err)
	}

	// Add a new Wifi network
	req, err = http.NewRequest("POST", "/api/wifi", &bTmp)
	if err != nil {
		t.Fatal(err)
	}

	response = executeRequest(h, req)
	if response.Code != http.StatusCreated {
		t.Errorf("Expected status %d, got %d", http.StatusCreated, response.Code)
	}

	// Get the Wifi network
	req, err = http.NewRequest("GET", "/api/wifi/test", nil)
	if err != nil {
		t.Fatal(err)
	}

	response = executeRequest(h, req)
	if response.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, response.Code)
	}

	jsonapi.UnmarshalPayload(response.Body, &testWifi)
	if err != nil {
		t.Fatal(err)
	}

	if testWifi.Ssid != "test" {
		t.Errorf("Expected SSID %s, got %s", "test", testWifi.Ssid)
	}
	if testWifi.SecurityKey != "key" {
		t.Errorf("Expected SecurityKey %s, got %s", "key", testWifi.SecurityKey)
	}

	// Update the Wifi network
	testWifi.SecurityKey = "newkey"

	err = jsonapi.MarshalPayload(&bTmp, &testWifi)
	if err != nil {
		t.Fatal(err)
	}

	req, err = http.NewRequest("PUT", "/api/wifi/test", &bTmp)
	if err != nil {
		t.Fatal(err)
	}

	response = executeRequest(h, req)
	if response.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, response.Code)
	}

	// Get the Wifi network
	req, err = http.NewRequest("GET", "/api/wifi/test", nil)
	if err != nil {
		t.Fatal(err)
	}

	response = executeRequest(h, req)
	if response.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, response.Code)
	}

	err = jsonapi.UnmarshalPayload(response.Body, &testWifi)
	if err != nil {
		t.Fatal(err)
	}

	if testWifi.Ssid != "test" {
		t.Errorf("Expected SSID %s, got %s", "test", testWifi.Ssid)
	}
	if testWifi.SecurityKey != "newkey" {
		t.Errorf("Expected SecurityKey %s, got %s", "newkey", testWifi.SecurityKey)
	}

	// Delete the Wifi network
	req, err = http.NewRequest("DELETE", "/api/wifi/test", nil)
	if err != nil {
		t.Fatal(err)
	}

	response = executeRequest(h, req)
	if response.Code != http.StatusNoContent {
		t.Errorf("Expected status %d, got %d", http.StatusOK, response.Code)
	}

	// Get the Wifi network
	req, err = http.NewRequest("GET", "/api/wifi/test", nil)
	if err != nil {
		t.Fatal(err)
	}

	response = executeRequest(h, req)
	if response.Code != http.StatusNotFound {
		t.Errorf("Expected status %d, got %d", http.StatusNotFound, response.Code)
	}
}

func executeRequest(h *controller.HttpHandler, req *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	(*h).ServeHTTP(rr, req)
	return rr
}
