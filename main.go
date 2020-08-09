package main

import "fmt"

func serializeBoard(board [8][8]int) []byte {
	serialized := []byte{}
	for _, row := range board {
		for _, square := range row {
			serialized = append(serialized, byte(square))
		}
	}
	return serialized
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
	fmt.Println(serializeBoard(board))
}
