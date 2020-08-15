package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"math"
	"net/http"
	"strconv"

	"github.com/fatih/color"
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
		{blackRook, blackKnight, blackBishop, blackQueen, blackKing, blackBishop, blackKnight, blackRook},
		{blackPawn, blackPawn, blackPawn, blackPawn, blackPawn, blackPawn, blackPawn, blackPawn},
		{empty, empty, empty, empty, empty, empty, empty, empty},
		{empty, empty, empty, empty, empty, empty, empty, empty},
		{empty, empty, empty, empty, empty, empty, empty, empty},
		{empty, empty, empty, empty, empty, empty, empty, empty},
		{whitePawn, whitePawn, whitePawn, whitePawn, whitePawn, whitePawn, whitePawn, whitePawn},
		{whiteRook, whiteKnight, whiteBishop, whiteQueen, whiteKing, whiteBishop, whiteKnight, whiteRook},
	}
	return board
}

func main() {
	fmt.Println("Starting backend.")
	api()
}

func readConfig() Config {
	if fileExists("config.json") {
		bytes, err := ioutil.ReadFile("config.json")
		check(err)
		config := Config{}
		json.Unmarshal(bytes, &config)
		return config
	}
	jsonBytes, parseErr := json.MarshalIndent(defaultConfig, "", "   ")
	check(parseErr)

	writeErr := ioutil.WriteFile("config.json", jsonBytes, 0700)
	check(writeErr)
	return defaultConfig
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

type squareDiff struct {
	Row     int `json:"row"`
	Column  int `json:"column"`
	Removed int `json:"removed"`
	Added   int `json:"added"`
}

func printDiff(diff squareDiff) {
	printString := strconv.Itoa(diff.Row) + " " + strconv.Itoa(diff.Column) + " " + strconv.Itoa(diff.Removed) + "=>" + strconv.Itoa(diff.Added)
	if diff.Added == empty {
		color.Red(printString)
	} else {
		color.Green(printString)
	}
}

func pieceColor(piece int) string {
	switch piece {
	case blackPawn, blackKnight, blackBishop, blackRook, blackQueen, blackKing:
		return "BLACK"
	case whitePawn, whiteKnight, whiteBishop, whiteRook, whiteQueen, whiteKing:
		return "WHITE"
	default:
		return "INVALID"
	}
}

/*
- Only one piece may move at a time
- Player may only move their own pieces (edge case: castling)
- Pieces may only move to the squares they are allowed to move
*/
func isValidMove(oldState [8][8]int, newState [8][8]int, moveAuthor string) bool {
	squareDiffs := []squareDiff{}

	for i := range oldState {
		for j := range oldState {
			if oldState[i][j] != newState[i][j] {
				diff := squareDiff{
					Row:     i,
					Column:  j,
					Removed: oldState[i][j],
					Added:   newState[i][j],
				}
				squareDiffs = append(squareDiffs, diff)
				printDiff(diff)
			}
		}
	}

	var pieceMoved int
	if len(squareDiffs) > 2 {
		fmt.Println("Expected square diff of length<=2, but received length " + strconv.Itoa(len(squareDiffs)))
		return false
	}
	if len(squareDiffs) == 2 {
		for _, diff := range squareDiffs {
			if diff.Added == empty {
				pieceMoved = diff.Removed
			}
		}
		if squareDiffs[0].Added == empty {
			pieceMoved = squareDiffs[0].Removed
		}
	}
	if len(squareDiffs) <= 1 {
		fmt.Println("Expected square diff of length<=2, but received length " + strconv.Itoa(len(squareDiffs)))
		return false
	}

	if pieceColor(pieceMoved) != moveAuthor {
		fmt.Println("User did not move their own piece.")
		return false
	}
	if !legalMoveForPiece(pieceMoved, squareDiffs, newState) {
		fmt.Println("Illegal move for piece " + strconv.Itoa(pieceMoved))
		return false
	}

	return true
}

func squaresBetweenClear(piece int, startPos []int, endPos []int, boardState [8][8]int) bool {
	for i := startPos[0]; i < endPos[0]; i++ {
		for j := startPos[1]; j < endPos[1]; j++ {
			fmt.Println(boardState[i][j])
		}
	}

	return false
}

func legalMoveForPiece(piece int, move []squareDiff, boardState [8][8]int) bool {
	startPos := []int{}
	endPos := []int{}
	var pieceTaken int
	if move[0].Added == 88 {
		startPos = []int{move[0].Row, move[0].Column}
		endPos = []int{move[1].Row, move[1].Column}
		if move[1].Removed != 88 {
			pieceTaken = move[1].Removed
		}
	}
	if move[1].Added == 88 {
		startPos = []int{move[1].Row, move[1].Column}
		endPos = []int{move[0].Row, move[0].Column}
		if move[0].Removed != 88 {
			pieceTaken = move[0].Removed
		}
	}

	rowCheck := startPos[0] - endPos[0]
	colCheck := startPos[1] - endPos[1]
	fmt.Println(startPos, endPos, pieceTaken)
	fmt.Println(rowCheck, colCheck)

	switch piece {
	case whitePawn:
		if (rowCheck == 1 || rowCheck == 2) && colCheck == 0 && pieceTaken == 0 {
			return true
		}
		if (rowCheck == 1 || rowCheck == 2) && (colCheck == 1 || colCheck == -1) && pieceTaken != 0 {
			return true
		}
		return false
	case blackPawn:
		if (rowCheck == -1 || rowCheck == -2) && colCheck == 0 && pieceTaken == 0 {
			fmt.Println(squaresBetweenClear(piece, startPos, endPos, boardState))
			return true
		}
		if (rowCheck == -1 || rowCheck == -2) && (colCheck == 1 || colCheck == -1) && pieceTaken != 0 {
			return true
		}
		return false
	case whiteKnight, blackKnight:
		if rowCheck == 2 && colCheck == -1 {
			return true
		}
		if rowCheck == 2 && colCheck == 1 {
			return true
		}
		if rowCheck == 1 && colCheck == 2 {
			return true
		}
		if rowCheck == 1 && colCheck == -2 {
			return true
		}
		if rowCheck == -1 && colCheck == -2 {
			return true
		}
		if rowCheck == -1 && colCheck == 2 {
			return true
		}
		if rowCheck == -2 && colCheck == 1 {
			return true
		}
		if rowCheck == -2 && colCheck == -1 {
			return true
		}
		return false
	case whiteBishop, blackBishop:
		if math.Abs(float64(rowCheck)) == math.Abs(float64(colCheck)) {
			return true
		}
		return false
	case whiteRook, blackRook:
		if rowCheck == 0 || colCheck == 0 {
			return true
		}
		return false
	case whiteQueen, blackQueen:
		if math.Abs(float64(rowCheck)) == math.Abs(float64(colCheck)) || (rowCheck == 0 || colCheck == 0) {
			return true
		}
		return false
	case whiteKing, blackKing:
		if rowCheck == 1 && colCheck == 0 {
			return true
		}
		if rowCheck == -1 && colCheck == 0 {
			return true
		}
		if rowCheck == 0 && colCheck == 1 {
			return true
		}
		if rowCheck == 0 && colCheck == -1 {
			return true
		}
		if rowCheck == 1 && colCheck == 1 {
			return true
		}
		if rowCheck == 1 && colCheck == -1 {
			return true
		}
		if rowCheck == -1 && colCheck == 1 {
			return true
		}
		if rowCheck == -1 && colCheck == -1 {
			return true
		}
		return false
	default:
		return true
	}
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

				for _, sub := range socketSubs {
					if sub.GameID == gameID {
						sub.Conn.WriteJSON(game)
					}
				}
			} else {
				fmt.Println("Signature didn't verify properly.")
				return
			}
		} else {
			fmt.Println("There's already a player for " + jsonBody.Side)
		}
	})
}
