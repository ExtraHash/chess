package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"strconv"

	uuid "github.com/satori/go.uuid"

	_ "github.com/jinzhu/gorm/dialects/mysql"
	_ "github.com/jinzhu/gorm/dialects/sqlite"

	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
)

var config = readConfig()
var db = getDB(config)
var socketSubs = []SocketSub{}

// SocketSub is a subscription to a socket.
type SocketSub struct {
	GameID uuid.UUID       `json:"gameID"`
	Conn   *websocket.Conn `json:"-"`
}

// JoinRequest is a request to join a game.
type JoinRequest struct {
	PubKey string `json:"pubKey"`
	Signed string `json:"signed"`
	Side   string `json:"side"`
}

func check(e error) {
	if e != nil {
		panic(e)
	}
}

func serializeBoard(board [8][8]int) []byte {
	serialized := []byte{}
	for _, row := range board {
		for _, square := range row {
			serialized = append(serialized, byte(square))
		}
	}
	return serialized
}

func deserializeBoard(dat []byte) [8][8]int {
	board := [8][8]int{}
	i := 0
	j := 0
	for _, square := range dat {
		board[i][j] = int(square)
		j++
		if j == 8 {
			j = 0
			i++
		}
		if i == 8 {
			break
		}
	}
	return board
}

func createBoard() [8][8]int {
	board := [8][8]int{
		{blackRook, blackKnight, blackBishop, blackKing, blackQueen, blackBishop, blackKnight, blackRook},
		{blackPawn, blackPawn, blackPawn, blackPawn, blackPawn, blackPawn, blackPawn, blackPawn},
		{empty, empty, empty, empty, empty, empty, empty, empty},
		{empty, empty, empty, empty, empty, empty, empty, empty},
		{empty, empty, empty, empty, empty, empty, empty, empty},
		{empty, empty, empty, empty, empty, empty, empty, empty},
		{whitePawn, whitePawn, whitePawn, whitePawn, whitePawn, whitePawn, whitePawn, whitePawn},
		{whiteRook, whiteKnight, whiteBishop, whiteKing, whiteQueen, whiteBishop, whiteKnight, whiteRook},
	}
	return board
}

func main() {
	fmt.Println("Starting backend.")
	api()
}

func readConfig() Config {
	bytes, err := ioutil.ReadFile("config.json")
	check(err)
	config := Config{}
	json.Unmarshal(bytes, &config)
	return config
}

// GetIP from http request
func GetIP(r *http.Request) string {
	forwarded := r.Header.Get("X-FORWARDED-FOR")
	if forwarded != "" {
		return forwarded
	}
	return r.RemoteAddr
}

// SocketHandler handles the websocket connection messages and responses.
func SocketHandler() http.Handler {
	return http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		fmt.Println(req.Method, req.URL, GetIP(req))

		vars := mux.Vars(req)
		id := vars["id"]

		gameID, err := uuid.FromString(id)
		if err != nil {
			fmt.Println("Invalid gameID.")
			return
		}

		var upgrader = websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
		}

		upgrader.CheckOrigin = func(req *http.Request) bool { return true }

		conn, err := upgrader.Upgrade(res, req, nil)

		if err != nil {
			fmt.Println(err)
			res.Write([]byte("the client is not using the websocket protocol: 'upgrade' token not found in 'Connection' header"))
			return
		}

		fmt.Println("Incoming websocket connection.")

		socketSub := SocketSub{
			GameID: gameID,
			Conn:   conn,
		}
		socketSubs = append(socketSubs, socketSub)

		fmt.Println("Added subscription to list.")
	})
}

func api() {
	router := mux.NewRouter()
	router.Handle("/game", GamePostHandler()).Methods("POST")
	router.Handle("/game", GamePatchHandler()).Methods("PATCH")
	router.Handle("/game/{id}", GameGetHandler()).Methods("GET")
	router.Handle("/join/{id}", JoinPostHandler()).Methods("POST")
	router.Handle("/socket/{id}", SocketHandler()).Methods("GET")

	http.Handle("/", router) // enable the router
	port := ":" + strconv.Itoa(config.Port)
	fmt.Println("\nListening on port " + port)
	log.Fatal(http.ListenAndServe(port, handlers.CORS(handlers.AllowedHeaders([]string{"X-Requested-With", "Content-Type", "Authorization"}), handlers.AllowedMethods([]string{"GET", "POST", "PUT", "HEAD", "OPTIONS", "PATCH"}), handlers.AllowedOrigins([]string{"*"}))(router)))
}

// GamePostResponse is a response to the /game endpoint.
type GamePostResponse struct {
	GameID uuid.UUID `json:"gameID"`
	Board  [8][8]int `json:"board"`
}

// GameGetResponse is a response to the /game endpoint.
type GameGetResponse struct {
	GameID uuid.UUID   `json:"gameID"`
	State  [][8][8]int `json:"state"`
}

func storeBoardState(gameID uuid.UUID, state [8][8]int, moveAuthor string) {
	db.Create(&BoardState{
		GameID:     gameID,
		State:      serializeBoard(state),
		MoveAuthor: moveAuthor,
	})
}

// GameStatePush is a websocket notification of a new game state.
type GameStatePush struct {
	GameID uuid.UUID `json:"gameID"`
	Board  [8][8]int `json:"board"`
	Type   string    `json:"type"`
}

// GamePatchHandler handles the game endpoint.
func GamePatchHandler() http.Handler {
	return http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		fmt.Println(req.Method, req.URL, GetIP(req))

		body, err := ioutil.ReadAll(req.Body)
		if err != nil {
			panic(err)
		}

		var jsonBody ReceivedBoardState
		json.Unmarshal(body, &jsonBody)

		game := Game{}
		db.First(&game, "game_id = ?", jsonBody.GameID)

		lastMove := BoardState{}
		db.Last(&lastMove, "game_id = ?", jsonBody.GameID)

		sig, err := hex.DecodeString(jsonBody.Signed)
		if err != nil {
			fmt.Println("signature is not valid hex string.")
			return
		}

		verified := false

		if lastMove.MoveAuthor == "WHITE" {
			if len(game.BlackPlayer) == 0 {
				fmt.Println("There is no black player!")
				return
			}
			verified = ed25519.Verify(game.BlackPlayer, serializeBoard(jsonBody.State), sig)
		}

		if lastMove.MoveAuthor == "BLACK" {
			if len(game.WhitePlayer) == 0 {
				fmt.Println("There is no white player!")
				return
			}
			verified = ed25519.Verify(game.WhitePlayer, serializeBoard(jsonBody.State), sig)
		}

		if !verified {
			fmt.Println("Invalid signature for move.")
			return
		}

		var newMoveAuthor string
		if lastMove.MoveAuthor == "BLACK" {
			newMoveAuthor = "WHITE"
		}
		if lastMove.MoveAuthor == "WHITE" {
			newMoveAuthor = "BLACK"
		}

		if isValidMove(deserializeBoard(lastMove.State), jsonBody.State, newMoveAuthor) {
			newState := BoardState{
				GameID:     jsonBody.GameID,
				State:      serializeBoard(jsonBody.State),
				MoveAuthor: newMoveAuthor,
			}

			db.Create(&newState)

			broadcastState := GameStatePush{
				GameID: jsonBody.GameID,
				Board:  jsonBody.State,
				Type:   "move",
			}

			for _, sub := range socketSubs {
				if sub.GameID == jsonBody.GameID {
					// send the new state
					sub.Conn.WriteJSON(broadcastState)
				}
			}
		} else {
			fmt.Println("Move is not valid.")
			return
		}

	})
}

/*
- Only one piece may move at a time
- Player may only move their own pieces (edge case: castling)
- Pieces may only move to the squares they are allowed to move
*/
func isValidMove(oldState [8][8]int, newState [8][8]int, moveAuthor string) bool {
	fmt.Println("AUTHOR", moveAuthor)
	fmt.Println("OLD STATE", oldState)
	fmt.Println("NEW STATE", newState)

	for i := range oldState {
		for j := range oldState {
			if oldState[i][j] != newState[i][j] {
				fmt.Println(i, j, oldState[i][j], newState[i][j])
			}
		}
	}

	return true
}

// GameGetHandler handles the get method on the game endpoint.
func GameGetHandler() http.Handler {
	return http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		fmt.Println(req.Method, req.URL, GetIP(req))

		vars := mux.Vars(req)
		gameID, err := uuid.FromString(vars["id"])
		if err != nil {
			fmt.Println("bad game ID")
			return
		}
		game := Game{}
		db.Where("game_id = ?", gameID).First(&game)

		var state [][8][8]int
		boardStates := []BoardState{}
		db.Where("game_id = ?", game.GameID).Find(&boardStates)

		for _, row := range boardStates {
			state = append(state, deserializeBoard(row.State))
		}

		response := GameGetResponse{
			GameID: game.GameID,
			State:  state,
		}

		byteRes, err := json.Marshal(response)
		check(err)
		res.Write(byteRes)
	})
}

// GamePostHandler handles the game endpoint.
func GamePostHandler() http.Handler {
	return http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		fmt.Println(req.Method, req.URL, GetIP(req))

		game := Game{
			GameID: uuid.NewV4(),
		}
		db.Create(&game)
		storeBoardState(game.GameID, createBoard(), "BLACK")
		// res.Header().Set("Content-Type", "application/x-msgpack")
		res.Header().Set("Content-Type", "application/json")
		byteRes, err := json.Marshal(game)
		check(err)
		res.Write(byteRes)
	})
}

// JoinPostHandler handles the post endpoint.
func JoinPostHandler() http.Handler {
	return http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		fmt.Println(req.Method, req.URL, GetIP(req))

		vars := mux.Vars(req)
		gameID, err := uuid.FromString(vars["id"])
		if err != nil {
			fmt.Println("bad game ID")
			return
		}

		body, err := ioutil.ReadAll(req.Body)
		if err != nil {
			panic(err)
		}

		var jsonBody JoinRequest
		json.Unmarshal(body, &jsonBody)

		game := Game{}
		db.First(&game, "game_id = ?", gameID)

		var requestedSide ed25519.PublicKey
		if jsonBody.Side == "WHITE" {
			requestedSide = game.WhitePlayer
		}
		if jsonBody.Side == "BLACK" {
			requestedSide = game.BlackPlayer
		}

		if len(requestedSide) == 0 {
			fmt.Println("Nobody is playing " + jsonBody.Side + " currently, checking signature.")
			sig, err := hex.DecodeString(jsonBody.Signed)
			if err != nil {
				fmt.Println("Signature is not valid hex string.")
				return
			}

			pubKey, err := hex.DecodeString(jsonBody.PubKey)
			if err != nil {
				fmt.Println("Public key is not valid hex string.")
				return
			}

			if ed25519.Verify(pubKey, []byte(gameID.String()), sig) {
				fmt.Println("Player successfully joined as " + jsonBody.Side)
				if jsonBody.Side == "WHITE" {
					game.WhitePlayer = pubKey
				}
				if jsonBody.Side == "BLACK" {
					game.BlackPlayer = pubKey
				}
				db.Save(&game)
			} else {
				fmt.Println("Signature didn't verify properly.")
				return
			}
		} else {
			fmt.Println("There's already a player for " + jsonBody.Side)
		}
	})
}
