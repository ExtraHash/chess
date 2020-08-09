package main

import (
	"time"

	"github.com/jinzhu/gorm"
	uuid "github.com/satori/go.uuid"
)

// Config is the config file for the db and api
type Config struct {
	DbType          string `json:"dbType"`
	DbConnectionStr string `json:"dbConnectionStr"`
	Port            int    `json:"port"`
}

// Game is an individual chess game.
type Game struct {
	Model
	GameID uuid.UUID `json:"gameID"`
}

// BoardState is a single moment in time for a chess board
type BoardState struct {
	Model
	GameID uuid.UUID `json:"gameID"`
	State  []byte    `json:"state"`
}

// ReceivedBoardState is a new board state received from the client.
type ReceivedBoardState struct {
	GameID uuid.UUID `json:"gameID"`
	State  [8][8]int `json:"state"`
}

// Model that hides unnecessary fields in json
type Model struct {
	ID        uint       `json:"-" gorm:"primary_key"`
	CreatedAt time.Time  `json:"-"`
	UpdatedAt time.Time  `json:"-"`
	DeletedAt *time.Time `json:"-" sql:"index"`
}

func getDB(config Config) *gorm.DB {
	// initialize database, support sqlite and mysql
	db, err := gorm.Open(config.DbType, config.DbConnectionStr)
	check(err)

	db.AutoMigrate(Game{})
	db.AutoMigrate(BoardState{})

	return db
}
