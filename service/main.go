package service

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jacobalberty/beenfar/service/controller"
	"github.com/jacobalberty/beenfar/service/model"
)

func NewBeenFarService() *BeenFarService {
	var bfs = &BeenFarService{
		configData: model.NewConfigData(),
		devices:    model.NewDevices(),
	}
	bfs.Init()
	return bfs
}

type BeenFarService struct {
	configData *model.ConfigData
	devices    *model.Devices
	h          *chi.Mux
}

// Initialize the BeenFar service and register all devices and handlers
func (b *BeenFarService) Init() {
	b.h = chi.NewRouter()

	h := &controller.HttpHandler{}
	h.Init(b.h, b.configData, b.devices)

	unifi := &controller.UnifiHandler{}
	unifi.Init(b.h, b.configData, b.devices)

}

func (b *BeenFarService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b.h.ServeHTTP(w, r)
}
