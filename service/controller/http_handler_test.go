package controller_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/google/jsonapi"
	"github.com/jacobalberty/beenfar/service"
	"github.com/jacobalberty/beenfar/service/model"
)

func TestWifiNetworkList(t *testing.T) {
	var (
		err          error
		req          *http.Request
		h            *service.BeenFarService
		bTmp         bytes.Buffer
		response     *httptest.ResponseRecorder
		wifiNetworks []interface{}
	)

	t.Parallel()

	h = service.NewBeenFarService()
	h.Init()

	// Get an empty wifi network list
	if req, err = http.NewRequest("GET", "/api/wifi", nil); err != nil {
		t.Fatal(err)
	}

	response = executeRequest(h, req)
	if response.Code != http.StatusOK {
		t.Errorf("Expected status code %v, got %v", http.StatusOK, response.Code)
	}

	if wifiNetworks, err = jsonapi.UnmarshalManyPayload(response.Body, reflect.TypeOf(new(model.WifiNetworkConfig))); err != nil {
		t.Fatal(err)
	}

	if len(wifiNetworks) != 0 {
		t.Errorf("Expected empty wifi network list, got %v", wifiNetworks)

	}

	// Add a wifi network
	wifiNetworks = append(wifiNetworks,
		&model.WifiNetworkConfig{
			Ssid: "TestWifiNetwork",
		},
		&model.WifiNetworkConfig{
			Ssid: "TestWifiNetwork2",
		},
	)

	for _, wifiNetwork := range wifiNetworks {
		bTmp.Reset()

		err = jsonapi.MarshalPayload(&bTmp, wifiNetwork)
		if err != nil {
			t.Fatal(err)
		}

		req, err = http.NewRequest("POST", "/api/wifi", &bTmp)
		if err != nil {
			t.Fatal(err)
		}

		response = executeRequest(h, req)
		if response.Code != http.StatusCreated {
			t.Errorf("Expected status code %v, got %v", http.StatusCreated, response.Code)
		}
	}

	// Get the wifi network list
	req, err = http.NewRequest("GET", "/api/wifi", nil)
	if err != nil {
		t.Fatal(err)
	}

	response = executeRequest(h, req)
	if response.Code != http.StatusOK {
		t.Errorf("Expected status code %v, got %v", http.StatusOK, response.Code)
	}

	if wifiNetworks, err = jsonapi.UnmarshalManyPayload(response.Body, reflect.TypeOf(new(model.WifiNetworkConfig))); err != nil {
		t.Fatal(err)
	}

	if len(wifiNetworks) != 2 {
		t.Errorf("Expected wifi network list with 2 elements, got %v", wifiNetworks)
	}

	// Delete the wifi network list
	for _, wifiNetwork := range wifiNetworks {
		ssid := wifiNetwork.(*model.WifiNetworkConfig).Ssid
		req, err = http.NewRequest("DELETE", "/api/wifi/"+ssid, nil)
		if err != nil {
			t.Fatal(err)
		}

		response = executeRequest(h, req)
		if response.Code != http.StatusNoContent {
			t.Errorf("Expected status code %v, got %v", http.StatusNoContent, response.Code)
		}
	}

}

func TestWifiNetwork(t *testing.T) {
	var (
		h        *service.BeenFarService
		bTmp     bytes.Buffer
		response *httptest.ResponseRecorder
		testWifi model.WifiNetworkConfig
	)
	t.Parallel()

	h = service.NewBeenFarService()
	h.Init()

	// With a non-existent SSID
	req, err := http.NewRequest("GET", "/api/wifi/test", nil)
	if err != nil {
		t.Fatal(err)
	}

	response = executeRequest(h, req)
	if response.Code != http.StatusNotFound {
		t.Errorf("Expected status %d, got %d", http.StatusNotFound, response.Code)
	}

	testWifi = model.WifiNetworkConfig{
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

	err = jsonapi.UnmarshalPayload(response.Body, &testWifi)
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

func executeRequest(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}
