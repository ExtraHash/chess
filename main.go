package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strconv"

	uuid "github.com/satori/go.uuid"

	_ "github.com/jinzhu/gorm/dialects/mysql"

	"github.com/gorilla/mux"
)

var config = readConfig()
var db = getDB(config)

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

func api() {
	router := mux.NewRouter()
	router.Handle("/game", GamePostHandler()).Methods("POST")
	router.Handle("/game/{id}", GameGetHandler()).Methods("GET")
	router.Handle("/game", GamePatchHandler()).Methods("PATCH")

	http.Handle("/", router) // enable the router
	port := ":" + strconv.Itoa(config.Port)
	fmt.Println("\nListening on port " + port)
	err := http.ListenAndServe(port, router) // mux.Router now in play
	check(err)
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

func storeBoardState(gameID uuid.UUID, state [8][8]int) {
	db.Create(&BoardState{
		GameID: gameID,
		State:  serializeBoard(state),
	})
}

// GamePatchHandler handles the game endpoint.
func GamePatchHandler() http.Handler {
	return http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		body, err := ioutil.ReadAll(req.Body)
		if err != nil {
			panic(err)
		}

		var jsonBody ReceivedBoardState
		json.Unmarshal(body, &jsonBody)

		fmt.Println(jsonBody.GameID)
		fmt.Println(jsonBody.State)

		newState := BoardState{
			GameID: jsonBody.GameID,
			State:  serializeBoard(jsonBody.State),
		}

		db.Create(&newState)
	})
}

// GameGetHandler handles the get method on the game endpoint.
func GameGetHandler() http.Handler {
	return http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
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
		game := Game{
			GameID: uuid.NewV4(),
		}
		db.Create(&game)
		storeBoardState(game.GameID, createBoard())
		// res.Header().Set("Content-Type", "application/x-msgpack")
		res.Header().Set("Content-Type", "application/json")
		byteRes, err := json.Marshal(game)
		check(err)
		res.Write(byteRes)
	})
}
