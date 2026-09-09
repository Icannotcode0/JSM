package jsmHttp

import (
	"encoding/json"
	"log"
	"net/http"
)

type clientError struct {
	Message string `json:"error"`
}

func WriteJSONError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(clientError{Message: msg}); err != nil {
		log.Printf("write json error response: %v", err)
	}
}

func WriteJSON(w http.ResponseWriter, data any, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("write json error response: %v", err)
	}
}
