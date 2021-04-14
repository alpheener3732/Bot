package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"sync"
	"time"
	"strings"

	"github.com/gorilla/mux"
)

// Manager holds a list of the crypto and stocks we are watching
type Manager struct {
	Watching map[string]*Stock
	sync.RWMutex
}

// NewManager stores all the information about the current stocks being watched and
// listens for api requests on 8080
func NewManager() *Manager {
	m := &Manager{
		Watching: make(map[string]*Stock, 0),
	}

	r := mux.NewRouter()
	r.HandleFunc("/ticker", m.AddStock).Methods("POST")
	r.HandleFunc("/ticker/{id}", m.DeleteStock).Methods("DELETE")
	r.HandleFunc("/ticker", m.GetStocks).Methods("GET")

	srv := &http.Server{
		Addr:         "localhost:8080",
		WriteTimeout: time.Second * 15,
		ReadTimeout:  time.Second * 15,
		IdleTimeout:  time.Second * 60,
		Handler:      r,
	}

	logger.Debugf("Starting api server on 8080...")

	// Run our server in a goroutine so that it doesn't block.
	go func() {
		if err := srv.ListenAndServe(); err != nil {
			log.Println(err)
		}
	}()

	return m
}

// StockRequest represents the json coming in from the request
type StockRequest struct {
	Ticker      string `json:"ticker"`
	Token       string `json:"discord_bot_token"`
	Name        string `json:"name"`
	Nickname    bool   `json:"set_nickname"`
	Crypto      bool   `json:"crypto"`
	Color       bool   `json:"set_color"`
	FlashChange bool   `json:"flash_change"`
	Frequency   int    `json:"frequency" default:"60"`
}

// AddStock adds a new stock or crypto to the list of what to watch
func (m *Manager) AddStock(w http.ResponseWriter, r *http.Request) {
	m.Lock()
	defer m.Unlock()

	logger.Debugf("Got an API request to add a ticker")

	// read body
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		logger.Errorf("Error: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "Error reading body: %v", err)
		return
	}

	// unmarshal into struct
	var stockReq StockRequest
	if err := json.Unmarshal(body, &stockReq); err != nil {
		logger.Errorf("Error unmarshalling: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "Error unmarshalling: %v", err)
		return
	}

	// ensure token is set
	if stockReq.Token == "" {
		logger.Error("Discord token required")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, "Error: token required")
		return
	}

	// ensure ticker is set
	if stockReq.Ticker == "" {
		logger.Error("Ticker required")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, "Error: ticker required")
		return
	}

	// add stock or crypto ticker
	if stockReq.Crypto {

		// ensure name is set
		if stockReq.Name == "" {
			logger.Error("Name required for crypto")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, "Error: Name required")
			return
		}

		// check if already existing
		if _, ok := m.Watching[strings.ToUpper(stockReq.Name)]; ok {
			logger.Error("Error: ticker already exists")
			w.WriteHeader(http.StatusConflict)
			return
		}

		stock := NewCrypto(stockReq.Ticker, stockReq.Token, stockReq.Name, stockReq.Nickname, stockReq.Color, stockReq.FlashChange, stockReq.Frequency)
		m.addStock(stockReq.Name, stock)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// check if already existing
	if _, ok := m.Watching[strings.ToUpper(stockReq.Ticker)]; ok {
		logger.Error("Error: ticker already exists")
		w.WriteHeader(http.StatusConflict)
		return
	}

	stock := NewStock(stockReq.Ticker, stockReq.Token, stockReq.Name, stockReq.Nickname, stockReq.Color, stockReq.FlashChange, stockReq.Frequency)
	m.addStock(stockReq.Ticker, stock)
	w.WriteHeader(http.StatusNoContent)
}

func (m *Manager) addStock(ticker string, stock *Stock) {
	stock.Ticker = strings.ToUpper(stock.Ticker)
	m.Watching[strings.ToUpper(ticker)] = stock
}

// DeleteStock addds a new stock or crypto to the list of what to watch
func (m *Manager) DeleteStock(w http.ResponseWriter, r *http.Request) {
	m.Lock()
	defer m.Unlock()

	logger.Debugf("Got an API request to delete a ticker")

	vars := mux.Vars(r)
	id := strings.ToUpper(vars["id"])

	if _, ok := m.Watching[id]; !ok {
		logger.Error("Error: no ticker found")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, "Error: ticker not found")
		return
	}
	// send shutdown sign
	m.Watching[id].Shutdown()

	// remove from cache
	delete(m.Watching, id)

	logger.Infof("Deleted ticker %s", id)
	w.WriteHeader(http.StatusNoContent)
}

// GetStocks returns a list of what the manager is watching
func (m *Manager) GetStocks(w http.ResponseWriter, r *http.Request) {
	m.RLock()
	defer m.RUnlock()
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(m.Watching); err != nil {
		logger.Errorf("Error serving request: %v", err)
		fmt.Fprintf(w, "Error: %v", err)
	}
}