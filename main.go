package main

import (
	"fmt"
	"io/ioutil"
	"log"
)

func check(e error) {
	if e != nil {
		log.Fatal(e)
	}
}

func writeBoardToDisk(board [8][8]int) {
	err := ioutil.WriteFile("board", serializeBoard(board), 0644)
	check(err)
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
		{rook, knight, bishop, king, queen, bishop, knight, rook},
		{pawn, pawn, pawn, pawn, pawn, pawn, pawn, pawn},
		{empty, empty, empty, empty, empty, empty, empty, empty},
		{empty, empty, empty, empty, empty, empty, empty, empty},
		{empty, empty, empty, empty, empty, empty, empty, empty},
		{empty, empty, empty, empty, empty, empty, empty, empty},
		{pawn, pawn, pawn, pawn, pawn, pawn, pawn, pawn},
		{rook, knight, bishop, king, queen, bishop, knight, rook},
	}
	return board
}

func main() {
	board := createBoard()
	fmt.Println("Created new board:")
	fmt.Println(board)

	fmt.Println("Saving board to disk.")
	writeBoardToDisk(board)

	fmt.Println("Loading board from disk.")
	newBoard := readBoardFromDisk()
	fmt.Println(newBoard)
}

func readBoardFromDisk() [8][8]int {
	dat, err := ioutil.ReadFile("board")
	check(err)
	return deserializeBoard(dat)
}
