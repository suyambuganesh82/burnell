package route

import (
	"log"
	"net/http"

	"github.com/gorilla/mux"

)

// NewRouter - create new router for HTTP routing
func NewRouter(mode *string) *mux.Router {

	router := mux.NewRouter().StrictSlash(true)
	for _, route := range GetEffectiveRoutes(mode) {
		var handler http.Handler

		handler = route.HandlerFunc
		handler = Logger(handler, route.Name)

		router.
			Methods(route.Method).
			Path(route.Pattern).
			Name(route.Name).
			Handler(route.AuthFunc(handler))

	}
	// TODO rate limit can be added per route basis
	router.Use(middleware.LimitRate)

	log.Println("router added")
	return router
}

// GetEffectiveRoutes gets effective routes
func GetEffectiveRoutes(mode *string) Routes {
	return append(PrometheusRoute, getRoutes(mode)...)
}

func getRoutes(mode *string) Routes {
	switch *mode {
	case util.Hybrid:
		return append(ReceiverRoutes, RestRoutes...)
	case util.Receiver:
		return ReceiverRoutes
	case util.TokenServer:
		return TokenServerRoutes
	default:
		return RestRoutes
	}
}