package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
)

var (
	itemSplit = ";"
)

// ImportBoard pulls in bots from the provided db
func (m *Manager) ImportBoard() {

	// query
	rows, err := m.DB.Query("SELECT clientID, token, name, nickname, color, crypto, header, items, frequency FROM boards")
	if err != nil {
		logger.Warningf("Unable to query tokens in db: %s", err)
		return
	}

	// load existing bots from db
	for rows.Next() {
		var importedBoard Board
		var itemsBulk string

		err = rows.Scan(&importedBoard.ClientID, &importedBoard.Token, &importedBoard.Name, &importedBoard.Nickname, &importedBoard.Color, &importedBoard.Crypto, &importedBoard.Header, &itemsBulk, &importedBoard.Frequency)
		if err != nil {
			logger.Errorf("Unable to load token from db: %s", err)
			continue
		}

		importedBoard.Items = strings.Split(itemsBulk, itemSplit)
		if importedBoard.Crypto {
			go importedBoard.watchCryptoPrice()
			m.StoreBoard(true, &importedBoard, false)
		} else {
			go importedBoard.watchStockPrice()
			m.StoreBoard(true, &importedBoard, false)
		}
		logger.Infof("Loaded board from db: %s", importedBoard.Name)
	}
	rows.Close()
}

// AddBoard adds a new board to the list of what to watch
func (m *Manager) AddBoard(w http.ResponseWriter, r *http.Request) {
	m.Lock()
	defer m.Unlock()

	logger.Debugf("Got an API request to add a board")

	// read body
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		logger.Errorf("Error: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "Error reading body: %v", err)
		return
	}

	// unmarshal into struct
	var boardReq Board
	if err := json.Unmarshal(body, &boardReq); err != nil {
		logger.Errorf("Error unmarshalling: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "Error unmarshalling: %v", err)
		return
	}

	// ensure token is set
	if boardReq.Token == "" {
		logger.Error("Discord token required")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, "Error: token required")
		return
	}

	// ensure frequency is set
	if boardReq.Frequency <= 0 {
		boardReq.Frequency = 60
	}

	// ensure name is set
	if boardReq.Name == "" {
		logger.Error("Board Name required")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, "Error: name required")
		return
	}

	// add stock or crypto ticker
	if boardReq.Crypto {

		// check if already existing
		if _, ok := m.WatchingBoard[boardReq.label()]; ok {
			logger.Error("Error: board already exists")
			w.WriteHeader(http.StatusConflict)
			return
		}

		go boardReq.watchCryptoPrice()
		m.StoreBoard(true, &boardReq, true)
	} else {

		// check if already existing
		if _, ok := m.WatchingBoard[boardReq.label()]; ok {
			logger.Error("Error: board already exists")
			w.WriteHeader(http.StatusConflict)
			return
		}

		go boardReq.watchStockPrice()
		m.StoreBoard(false, &boardReq, true)
	}

	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.WriteHeader(http.StatusOK)
	err = json.NewEncoder(w).Encode(boardReq)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
	}
	logger.Infof("Added board: %s\n", boardReq.Name)
}

func (m *Manager) StoreBoard(crypto bool, board *Board, update bool) {
	boardCount.Inc()
	id := board.label()
	m.WatchingBoard[id] = board

	var noDB *sql.DB
	if (m.DB == noDB) || !update {
		return
	}

	// query
	stmt, err := m.DB.Prepare("SELECT id FROM boards WHERE name = ? LIMIT 1")
	if err != nil {
		logger.Warningf("Unable to query board in db %s: %s", id, err)
		return
	}

	rows, err := stmt.Query(board.Name)
	if err != nil {
		logger.Warningf("Unable to query board in db %s: %s", id, err)
		return
	}

	var existingId int

	for rows.Next() {
		err = rows.Scan(&existingId)
		if err != nil {
			logger.Warningf("Unable to query board in db %s: %s", id, err)
			return
		}
	}
	rows.Close()

	if existingId != 0 {

		// update entry in db
		stmt, err := m.DB.Prepare("update boards set clientId = ?, token = ?, name = ?, nickname = ?, color = ?, crypto = ?, header = ?, items = ?, frequency = ? WHERE id = ?")
		if err != nil {
			logger.Warningf("Unable to update board in db %s: %s", id, err)
			return
		}

		res, err := stmt.Exec(board.ClientID, board.Token, board.Name, board.Nickname, board.Color, crypto, board.Header, strings.Join(board.Items, itemSplit), board.Frequency, existingId)
		if err != nil {
			logger.Warningf("Unable to update board in db %s: %s", id, err)
			return
		}

		_, err = res.LastInsertId()
		if err != nil {
			logger.Warningf("Unable to update board in db %s: %s", id, err)
			return
		}

		logger.Infof("Updated board in db %s", id)
	} else {

		// store new entry in db
		stmt, err := m.DB.Prepare("INSERT INTO boards(clientId, token, name, nickname, color, crypto, header, items, frequency) values(?,?,?,?,?,?,?,?,?)")
		if err != nil {
			logger.Warningf("Unable to store board in db %s: %s", id, err)
			return
		}

		res, err := stmt.Exec(board.ClientID, board.Token, board.Name, board.Nickname, board.Color, crypto, board.Header, strings.Join(board.Items, itemSplit), board.Frequency)
		if err != nil {
			logger.Warningf("Unable to store board in db %s: %s", id, err)
			return
		}

		_, err = res.LastInsertId()
		if err != nil {
			logger.Warningf("Unable to store board in db %s: %s", id, err)
			return
		}
	}
}

// DeleteBoard removes a board
func (m *Manager) DeleteBoard(w http.ResponseWriter, r *http.Request) {
	m.Lock()
	defer m.Unlock()

	logger.Debugf("Got an API request to delete a board")

	vars := mux.Vars(r)
	id := vars["id"]

	if _, ok := m.WatchingBoard[id]; !ok {
		logger.Error("Error: no ticker found")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, "Error: ticker not found")
		return
	}

	// send shutdown sign
	m.WatchingBoard[id].Close <- 1
	boardCount.Dec()

	var noDB *sql.DB
	if m.DB != noDB {
		// remove from db
		stmt, err := m.DB.Prepare("DELETE FROM boards WHERE name = ?")
		if err != nil {
			logger.Warningf("Unable to query board in db %s: %s", id, err)
			return
		}

		_, err = stmt.Exec(m.WatchingBoard[id].Name)
		if err != nil {
			logger.Warningf("Unable to query board in db %s: %s", id, err)
			return
		}
	}

	// remove from cache
	delete(m.WatchingBoard, id)

	logger.Infof("Deleted board %s", id)
	w.WriteHeader(http.StatusNoContent)
}

// RestartBoard stops and starts a board
func (m *Manager) RestartBoard(w http.ResponseWriter, r *http.Request) {
	m.Lock()
	defer m.Unlock()

	logger.Debugf("Got an API request to restart a board")

	vars := mux.Vars(r)
	id := vars["id"]

	if _, ok := m.WatchingBoard[id]; !ok {
		logger.Error("Error: no board found")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, "Error: board not found")
		return
	}

	// send shutdown sign
	m.WatchingBoard[id].Close <- 1

	// wait twice the update time
	time.Sleep(time.Duration(m.WatchingBoard[id].Frequency) * 2 * time.Second)

	// start the ticker again
	if m.WatchingBoard[id].Crypto {
		go m.WatchingBoard[id].watchCryptoPrice()
	} else {
		go m.WatchingBoard[id].watchStockPrice()
	}

	logger.Infof("Restarted ticker %s", id)
	w.WriteHeader(http.StatusNoContent)
}

// GetBoards returns a list of what the manager is watching
func (m *Manager) GetBoards(w http.ResponseWriter, r *http.Request) {
	m.RLock()
	defer m.RUnlock()
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(m.WatchingBoard); err != nil {
		logger.Errorf("Error serving request: %v", err)
		fmt.Fprintf(w, "Error: %v", err)
	}
}
