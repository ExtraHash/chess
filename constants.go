package main

var whitePawn int = 0x50
var whiteKnight int = 0x4e
var whiteBishop int = 0x42
var whiteRook int = 0x52
var whiteQueen int = 0x51
var whiteKing int = 0x4b

var blackPawn int = 0x70
var blackKnight int = 0x6e
var blackBishop int = 0x62
var blackRook int = 0x72
var blackQueen int = 0x71
var blackKing int = 0x6b

var empty int = 0x58

var whitePawnMoves = [][]int{{-1, 0}, {-1, 1}, {-1, -1}}
var blackPawnMoves = [][]int{{1, 0}, {1, 1}, {1, -1}}
var knightMoves = [][]int{{2, -1}, {2, 1}, {-2, 1}, {-2, -1}, {-1, 2}, {-1, -2}, {1, 2}, {1, -2}}
var kingMoves = [][]int{{1, 1}, {0, 1}, {-1, 1}, {-1, 0}, {-1, -1}, {0, -1}, {-1, 1}, {1, 0}}
var bishopMoves = []string{"NE", "NW", "SE", "SW"}
var rookMoves = []string{"N", "E", "S", "W"}
var queenMoves = []string{"N", "E", "S", "W", "NE", "NW", "SE", "SW"}
