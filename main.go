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

func finishEnPassant(boardState [8][8]int, moveAuthor string, endPos []int) [8][8]int {
	if moveAuthor == "WHITE" {
		boardState[endPos[0]+1][endPos[1]] = empty
	}
	if moveAuthor == "BLACK" {
		boardState[endPos[0]-1][endPos[1]] = empty
	}
	return boardState
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
		valid, pieceMoved, pieceTaken, startPos, endPos, castleType, enPassant := isValidMove(deserializeBoard(lastMove.State), jsonBody.State, newMoveAuthor, game.GameID)

		if castleType != "" {
			jsonBody.State = finishCastle(jsonBody.State, pieceColor(pieceMoved), castleType)
		}

		if enPassant {
			// modify state
			jsonBody.State = finishEnPassant(jsonBody.State, pieceColor(pieceMoved), endPos)
		}

		if valid {
			newState := BoardState{
				GameID:        jsonBody.GameID,
				State:         serializeBoard(jsonBody.State),
				MoveAuthor:    newMoveAuthor,
				PieceMoved:    pieceMoved,
				PieceTaken:    pieceTaken,
				StartPosition: posToString(startPos),
				EndPosition:   posToString(endPos),
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

func finishCastle(state [8][8]int, moveAuthor string, castleType string) [8][8]int {
	if moveAuthor == "WHITE" {
		if castleType == "KING" {
			state[7][5] = whiteRook
			state[7][7] = empty
		}
		if castleType == "QUEEN" {
			state[7][3] = whiteRook
			state[7][0] = empty
		}
	}
	if moveAuthor == "BLACK" {
		if castleType == "KING" {
			state[0][5] = blackRook
			state[0][7] = empty
		}

		if castleType == "QUEEN" {
			state[0][3] = blackRook
			state[0][0] = empty
		}
	}

	return state
}

type squareDiff struct {
	Row     int `json:"row"`
	Column  int `json:"column"`
	Removed int `json:"removed"`
	Added   int `json:"added"`
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
- Pushing pawn to last row promotes
- You may not move into check
- If you are in check you must remove check
-
*/
func isValidMove(oldState [8][8]int, newState [8][8]int, moveAuthor string, gameID uuid.UUID) (bool, int, int, []int, []int, string, bool) {
	squareDiffs := []squareDiff{}
	startPos := []int{}
	endPos := []int{}
	castleType := ""
	enPassant := false

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
			}
		}
	}

	var pieceMoved int
	var pieceTaken int
	if len(squareDiffs) > 2 {
		fmt.Println("Expected square diff of length<=2, but received length " + strconv.Itoa(len(squareDiffs)))
		return false, pieceMoved, pieceTaken, startPos, endPos, castleType, enPassant
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
		return false, pieceMoved, pieceTaken, startPos, endPos, castleType, enPassant
	}

	if pieceColor(pieceMoved) != moveAuthor {
		fmt.Println("User did not move their own piece.")
		return false, pieceMoved, pieceTaken, startPos, endPos, castleType, enPassant
	}
	legal, pieceTaken, sPos, ePos, cType, enPas := legalMoveForPiece(pieceMoved, squareDiffs, newState, moveAuthor, gameID)
	startPos = sPos
	endPos = ePos
	castleType = cType
	enPassant = enPas
	if !legal {
		fmt.Println("Illegal move for piece " + strconv.Itoa(pieceMoved))
		return false, pieceMoved, pieceTaken, startPos, endPos, castleType, enPassant
	}
	return true, pieceMoved, pieceTaken, startPos, endPos, castleType, enPassant
}

func rowToString(row int) string {
	switch row {
	case 0:
		return "8"
	case 1:
		return "7"
	case 2:
		return "6"
	case 3:
		return "5"
	case 4:
		return "4"
	case 5:
		return "3"
	case 6:
		return "2"
	case 7:
		return "1"
	}
	panic("Unknown row number.")
}

func colToString(col int) string {
	switch col {
	case 0:
		return "A"
	case 1:
		return "B"
	case 2:
		return "C"
	case 3:
		return "D"
	case 4:
		return "E"
	case 5:
		return "F"
	case 6:
		return "G"
	case 7:
		return "H"
	}
	panic("Unknown col number.")
}

func posToString(pos []int) string {
	if len(pos) != 2 {
		panic("position must be of length 2")
	}
	return colToString(pos[1]) + rowToString(pos[0])
}

func evaluateDirection(startPos []int, endPos []int) string {
	rowCheck := startPos[0] - endPos[0]
	colCheck := startPos[1] - endPos[1]

	if startPos[1] == endPos[1] && startPos[0] > endPos[0] {
		return "N"
	}
	if math.Abs(float64(rowCheck)) == math.Abs(float64(rowCheck)) && rowCheck > 0 && colCheck < 0 {
		return "NE"
	}
	if startPos[0] == endPos[0] && startPos[1] < endPos[1] {
		return "E"
	}
	if math.Abs(float64(rowCheck)) == math.Abs(float64(rowCheck)) && rowCheck < 0 && colCheck < 0 {
		return "SE"
	}
	if startPos[1] == endPos[1] && startPos[0] < endPos[0] {
		return "S"
	}
	if math.Abs(float64(rowCheck)) == math.Abs(float64(rowCheck)) && rowCheck < 0 && colCheck > 0 {
		return "SW"
	}
	if startPos[0] == endPos[0] && startPos[1] > endPos[1] {
		return "W"
	}
	if math.Abs(float64(rowCheck)) == math.Abs(float64(rowCheck)) && rowCheck > 0 && colCheck > 0 {
		return "NW"
	}

	return "INVALID"
}

func squaresBetweenClear(startPos []int, endPos []int, boardState [8][8]int) bool {
	direction := evaluateDirection(startPos, endPos)

	clear := true

	switch direction {
	case "N":
		for i := startPos[0] - 1; i > endPos[0]; i-- {
			if boardState[i][startPos[1]] != empty {
				clear = false
			}
		}
	case "NE":
		for i, j := startPos[0]-1, startPos[1]+1; i > endPos[0]; i, j = i-1, j+1 {
			if boardState[i][j] != empty {
				clear = false
			}
		}
	case "E":
		for i := startPos[1] + 1; i < endPos[1]; i++ {
			if boardState[startPos[0]][i] != empty {
				clear = false
			}
		}
	case "SE":
		for i, j := startPos[0]+1, startPos[1]+1; i < endPos[0]; i, j = i+1, j+1 {
			if boardState[i][j] != empty {
				clear = false
			}
		}
	case "S":
		for i := startPos[0] + 1; i < endPos[0]; i++ {
			if boardState[i][startPos[1]] != empty {
				clear = false
			}
		}
	case "SW":
		for i, j := startPos[0]+1, startPos[1]-1; i < endPos[0]; i, j = i+1, j-1 {
			if boardState[i][j] != empty {
				clear = false
			}
		}
	case "W":
		for i := startPos[1] - 1; i > endPos[1]; i-- {
			if boardState[startPos[0]][i] != empty {
				clear = false
			}
		}
	case "NW":
		for i, j := startPos[0]-1, startPos[1]-1; i > endPos[0]; i, j = i-1, j-1 {
			if boardState[i][j] != empty {
				clear = false
			}
		}
	}

	return clear
}

func legalEnPassant(gameID uuid.UUID, boardState [8][8]int, moveAuthor string, startPos []int, endPos []int) bool {
	if moveAuthor == "WHITE" {
		// is it starting from the correct row?
		if startPos[0] != 3 {
			return false
		}
		// is there a pawn on the row next to it?
		if boardState[endPos[0]+1][endPos[1]] != blackPawn {
			return false
		}
		// was the pawn just pushed?
		lastMove := BoardState{}
		db.Last(&lastMove, "game_id = ?", gameID)
		if lastMove.StartPosition != posToString([]int{endPos[0] - 1, endPos[1]}) {
			return false
		}
		return true
	}
	if moveAuthor == "BLACK" {
		// is it starting from the correct row?
		if startPos[0] != 4 {
			return false
		}
		// is there a pawn on the row next to it?
		if boardState[endPos[0]-1][endPos[1]] != whitePawn {
			return false
		}
		// was the pawn just pushed?
		lastMove := BoardState{}
		db.Last(&lastMove, "game_id = ?", gameID)
		if lastMove.StartPosition != posToString([]int{endPos[0] + 1, endPos[1]}) {
			return false
		}
		return true
	}
	return false
}

func checkStatus(boardState [8][8]int, color string) (bool, bool) {
	checkMate := false
	kingSquare := []int{}
	if color == "WHITE" {
		for i, row := range boardState {
			for j, square := range row {
				if square == whiteKing {
					kingSquare = []int{i, j}
				}
			}
		}
	}
	if color == "BLACK" {
		for i, row := range boardState {
			for j, square := range row {
				if square == blackKing {
					kingSquare = []int{i, j}
				}
			}
		}
	}

	return isAttacked(boardState, kingSquare, color), checkMate
}

func isAttacked(boardState [8][8]int, pos []int, color string) bool {
	attacked := false
	knightMoves := [][]int{{2, -1}, {2, 1}, {-2, 1}, {-2, -1}, {-1, 2}, {-1, -2}, {1, 2}, {1, -2}}

	if color == "WHITE" {
		// pawn check
		if pos[0] > 0 {
			if pos[1] > 0 {
				if boardState[pos[0]-1][pos[1]-1] == blackPawn {
					attacked = true
				}
			}
			if pos[1] < 7 {
				if boardState[pos[0]-1][pos[1]+1] == blackPawn {
					attacked = true
				}
			}
		}
		// knight check
		for _, move := range knightMoves {
			row := pos[0] + move[0]
			col := pos[1] + move[1]
			if row >= 0 && row <= 7 && col >= 0 && col <= 7 {
				if boardState[row][col] == blackKnight {
					attacked = true
				}
			}
		}
	}
	if color == "BLACK" {
		// pawn check
		if pos[0] < 7 {
			if pos[1] > 0 {
				if boardState[pos[0]+1][pos[1]-1] == whitePawn {
					attacked = true
				}
			}
			if pos[1] < 7 {
				if boardState[pos[0]+1][pos[1]+1] == whitePawn {
					attacked = true
				}
			}
		}
		// knight check
		for _, move := range knightMoves {
			row := pos[0] + move[0]
			col := pos[1] + move[1]
			if row >= 0 && row <= 7 && col >= 0 && col <= 7 {
				if boardState[row][col] == whiteKnight {
					attacked = true
				}
			}
		}
	}

	return attacked
}

func legalMoveForPiece(piece int, move []squareDiff, boardState [8][8]int, moveAuthor string, gameID uuid.UUID) (bool, int, []int, []int, string, bool) {
	startPos := []int{}
	endPos := []int{}
	cType := ""
	enPassant := false
	var pieceTaken int
	var pieceAdded int
	if move[0].Added == empty {
		startPos = []int{move[0].Row, move[0].Column}
		endPos = []int{move[1].Row, move[1].Column}
		if move[1].Removed != 88 {
			pieceTaken = move[1].Removed
		}
		pieceAdded = move[1].Added
	}
	if move[1].Added == empty {
		startPos = []int{move[1].Row, move[1].Column}
		endPos = []int{move[0].Row, move[0].Column}

		if move[0].Removed != empty {
			pieceTaken = move[0].Removed
		}
		pieceAdded = move[0].Added
	}

	if pieceTaken != empty {
		if moveAuthor == pieceColor(pieceTaken) {
			fmt.Println("Can not take your own piece.")
			return false, pieceTaken, startPos, endPos, cType, enPassant
		}
	}

	rowCheck := startPos[0] - endPos[0]
	colCheck := startPos[1] - endPos[1]

	if pieceAdded != piece {
		// only pawns can promote
		if piece != whitePawn && piece != blackPawn {
			return false, pieceTaken, startPos, endPos, cType, enPassant
		}
		// did the pawn move start from the second to last row?
		fmt.Println(startPos[0])
		if moveAuthor == "WHITE" {
			if startPos[0] != 1 {
				return false, pieceTaken, startPos, endPos, cType, enPassant
			}
		}
		if moveAuthor == "BLACK" {
			if startPos[0] != 6 {
				return false, pieceTaken, startPos, endPos, cType, enPassant
			}
		}
		// you can't promote to a king
		if pieceAdded == whiteKing || pieceAdded == blackKing {
			return false, pieceTaken, startPos, endPos, cType, enPassant
		}
	}

	check, checkMate := checkStatus(boardState, moveAuthor)
	fmt.Println(check, checkMate)
	if check {
		fmt.Println("Can not move own king into check.")
		return false, pieceTaken, startPos, endPos, cType, enPassant
	}

	switch piece {
	case whitePawn:
		if (rowCheck == 1 || rowCheck == 2) && colCheck == 0 && pieceTaken == 0 {
			if rowCheck == 2 {
				if startPos[0] == 6 {
					return squaresBetweenClear(startPos, endPos, boardState), pieceTaken, startPos, endPos, cType, enPassant
				}
				return false, pieceTaken, startPos, endPos, cType, enPassant
			}
			return squaresBetweenClear(startPos, endPos, boardState), pieceTaken, startPos, endPos, cType, enPassant
		}
		if (rowCheck == 1) && (colCheck == 1 || colCheck == -1) && pieceTaken != 0 {
			return true, pieceTaken, startPos, endPos, cType, enPassant
		}
		if (rowCheck == 1) && (colCheck == 1 || colCheck == -1) && pieceTaken == 0 {
			enPassant = true
			return legalEnPassant(gameID, boardState, moveAuthor, startPos, endPos), pieceTaken, startPos, endPos, cType, enPassant
		}
		return false, pieceTaken, startPos, endPos, cType, enPassant
	case blackPawn:
		if (rowCheck == -1 || rowCheck == -2) && colCheck == 0 && pieceTaken == 0 {
			if rowCheck == -2 {
				if startPos[0] == 1 {
					return squaresBetweenClear(startPos, endPos, boardState), pieceTaken, startPos, endPos, cType, enPassant
				}
				return false, pieceTaken, startPos, endPos, cType, enPassant
			}
			return squaresBetweenClear(startPos, endPos, boardState), pieceTaken, startPos, endPos, cType, enPassant
		}
		if (rowCheck == -1) && (colCheck == 1 || colCheck == -1) && pieceTaken != 0 {
			return true, pieceTaken, startPos, endPos, cType, enPassant
		}
		if (rowCheck == -1) && (colCheck == 1 || colCheck == -1) && pieceTaken == 0 {
			enPassant = true
			return legalEnPassant(gameID, boardState, moveAuthor, startPos, endPos), pieceTaken, startPos, endPos, cType, enPassant
		}
		return false, pieceTaken, startPos, endPos, cType, enPassant
	case whiteKnight, blackKnight:
		if rowCheck == 2 && colCheck == -1 {
			return true, pieceTaken, startPos, endPos, cType, enPassant
		}
		if rowCheck == 2 && colCheck == 1 {
			return true, pieceTaken, startPos, endPos, cType, enPassant
		}
		if rowCheck == 1 && colCheck == 2 {
			return true, pieceTaken, startPos, endPos, cType, enPassant
		}
		if rowCheck == 1 && colCheck == -2 {
			return true, pieceTaken, startPos, endPos, cType, enPassant
		}
		if rowCheck == -1 && colCheck == -2 {
			return true, pieceTaken, startPos, endPos, cType, enPassant
		}
		if rowCheck == -1 && colCheck == 2 {
			return true, pieceTaken, startPos, endPos, cType, enPassant
		}
		if rowCheck == -2 && colCheck == 1 {
			return true, pieceTaken, startPos, endPos, cType, enPassant
		}
		if rowCheck == -2 && colCheck == -1 {
			return true, pieceTaken, startPos, endPos, cType, enPassant
		}
		return false, pieceTaken, startPos, endPos, cType, enPassant
	case whiteBishop, blackBishop:
		if math.Abs(float64(rowCheck)) == math.Abs(float64(colCheck)) {
			return squaresBetweenClear(startPos, endPos, boardState), pieceTaken, startPos, endPos, cType, enPassant
		}
		return false, pieceTaken, startPos, endPos, cType, enPassant
	case whiteRook, blackRook:
		if rowCheck == 0 || colCheck == 0 {
			return squaresBetweenClear(startPos, endPos, boardState), pieceTaken, startPos, endPos, cType, enPassant
		}
		return false, pieceTaken, startPos, endPos, cType, enPassant
	case whiteQueen, blackQueen:
		if math.Abs(float64(rowCheck)) == math.Abs(float64(colCheck)) || (rowCheck == 0 || colCheck == 0) {
			return squaresBetweenClear(startPos, endPos, boardState), pieceTaken, startPos, endPos, cType, enPassant
		}
		return false, pieceTaken, startPos, endPos, cType, enPassant
	case whiteKing, blackKing:
		if rowCheck == 1 && colCheck == 0 {
			return true, pieceTaken, startPos, endPos, cType, enPassant
		}
		if rowCheck == -1 && colCheck == 0 {
			return true, pieceTaken, startPos, endPos, cType, enPassant
		}
		if rowCheck == 0 && colCheck == 1 {
			return true, pieceTaken, startPos, endPos, cType, enPassant
		}
		if rowCheck == 0 && colCheck == -1 {
			return true, pieceTaken, startPos, endPos, cType, enPassant
		}
		if rowCheck == 1 && colCheck == 1 {
			return true, pieceTaken, startPos, endPos, cType, enPassant
		}
		if rowCheck == 1 && colCheck == -1 {
			return true, pieceTaken, startPos, endPos, cType, enPassant
		}
		if rowCheck == -1 && colCheck == 1 {
			return true, pieceTaken, startPos, endPos, cType, enPassant
		}
		if rowCheck == -1 && colCheck == -1 {
			return true, pieceTaken, startPos, endPos, cType, enPassant
		}
		if rowCheck == 0 && colCheck == 2 {
			fmt.Println("Queenside castle detected.")
			cType = "QUEEN"
			return isLegalCastle("QUEEN", boardState, moveAuthor, gameID, startPos, endPos), pieceTaken, startPos, endPos, cType, enPassant
		}
		if rowCheck == 0 && colCheck == -2 {
			fmt.Println("Kingside castle detected.")
			cType = "KING"
			return isLegalCastle("KING", boardState, moveAuthor, gameID, startPos, endPos), pieceTaken, startPos, endPos, cType, enPassant
		}
		return false, pieceTaken, startPos, endPos, cType, enPassant
	default:
		return false, pieceTaken, startPos, endPos, cType, enPassant
	}
}

func isLegalCastle(direction string, boardState [8][8]int, moveAuthor string, gameID uuid.UUID, startPos []int, endPos []int) bool {
	kingPos := ""
	rookPos := ""

	if moveAuthor == "WHITE" {
		kingPos = "E1"
		if direction == "KING" {
			rookPos = "H1"
		}
		if direction == "QUEEN" {
			rookPos = "A1"
		}
	}
	if moveAuthor == "BLACK" {
		kingPos = "E8"
		if direction == "KING" {
			rookPos = "H8"
		}
		if direction == "QUEEN" {
			rookPos = "A8"
		}
	}

	kingMoveList := []BoardState{}
	rookMoveList := []BoardState{}

	// D8 is starting position of white king
	db.Where("game_id = ? AND start_position = ?", gameID, kingPos).Find(&kingMoveList)
	db.Where("game_id = ? AND start_position = ?", gameID, rookPos).Find(&rookMoveList)

	if len(kingMoveList) > 0 || len(rookMoveList) > 0 {
		return false
	}

	return squaresBetweenClear(startPos, endPos, boardState)
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
