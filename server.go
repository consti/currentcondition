package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Location represents a visitor's location
type Location struct {
	Lat       float64   `json:"lat"`
	Lng       float64   `json:"lng"`
	Timestamp time.Time `json:"timestamp"`
}

// Highscore represents a game high score entry
type Highscore struct {
	ID    int    `json:"id,omitempty"`
	Game  string `json:"game"`
	Name  string `json:"name"`
	Score int    `json:"score"`
}

// LocationStore holds unique visitor locations
type LocationStore struct {
	sync.RWMutex
	locations []Location
}

var store = &LocationStore{
	locations: make([]Location, 0),
}

var db *sql.DB

// Round coordinates to ~1km precision to group nearby visitors
func roundCoord(coord float64, precision int) float64 {
	mult := math.Pow(10, float64(precision))
	return math.Round(coord*mult) / mult
}

// Check if location already exists (within ~1km)
func (s *LocationStore) exists(lat, lng float64) bool {
	rLat := roundCoord(lat, 2)
	rLng := roundCoord(lng, 2)

	for _, loc := range s.locations {
		if roundCoord(loc.Lat, 2) == rLat && roundCoord(loc.Lng, 2) == rLng {
			return true
		}
	}
	return false
}

// Add location if it doesn't exist
func (s *LocationStore) Add(lat, lng float64) bool {
	s.Lock()
	defer s.Unlock()

	if s.exists(lat, lng) {
		return false
	}

	s.locations = append(s.locations, Location{
		Lat:       lat,
		Lng:       lng,
		Timestamp: time.Now(),
	})
	return true
}

// Get all locations
func (s *LocationStore) GetAll() []Location {
	s.RLock()
	defer s.RUnlock()

	result := make([]Location, len(s.locations))
	copy(result, s.locations)
	return result
}

func initDB() error {
	var err error
	db, err = sql.Open("sqlite3", "./crt-weather.db")
	if err != nil {
		return err
	}

	// Create highscores table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS highscores (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			game TEXT NOT NULL,
			name TEXT NOT NULL,
			score INTEGER NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_highscores_game_score ON highscores(game, score DESC);
	`)
	if err != nil {
		return err
	}

	// Create locations table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS locations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			lat REAL NOT NULL,
			lng REAL NOT NULL,
			lat_rounded REAL NOT NULL,
			lng_rounded REAL NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(lat_rounded, lng_rounded)
		);
	`)
	if err != nil {
		return err
	}

	// Initialize default scores for each game if empty
	games := []string{"SNAKE", "TETRIS", "ASTEROIDS", "PONG"}
	for _, game := range games {
		var count int
		err = db.QueryRow("SELECT COUNT(*) FROM highscores WHERE game = ?", game).Scan(&count)
		if err != nil {
			return err
		}
		if count == 0 {
			// Insert 5 default entries
			for i := 0; i < 5; i++ {
				_, err = db.Exec("INSERT INTO highscores (game, name, score) VALUES (?, 'CON', 0)", game)
				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func getHighscores(game string) ([]Highscore, error) {
	rows, err := db.Query(`
		SELECT id, game, name, score FROM highscores 
		WHERE game = ? 
		ORDER BY score DESC 
		LIMIT 5
	`, game)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var scores []Highscore
	for rows.Next() {
		var h Highscore
		if err := rows.Scan(&h.ID, &h.Game, &h.Name, &h.Score); err != nil {
			return nil, err
		}
		scores = append(scores, h)
	}

	// Ensure we always return 5 entries
	for len(scores) < 5 {
		scores = append(scores, Highscore{Game: game, Name: "CON", Score: 0})
	}

	return scores, nil
}

func saveHighscore(game, name string, score int) error {
	// Sanitize name to 3 uppercase letters
	name = strings.ToUpper(name)
	if len(name) > 3 {
		name = name[:3]
	}
	for len(name) < 3 {
		name += " "
	}

	// Insert the new score
	_, err := db.Exec("INSERT INTO highscores (game, name, score) VALUES (?, ?, ?)", game, name, score)
	if err != nil {
		return err
	}

	// Keep only top 5 scores per game
	_, err = db.Exec(`
		DELETE FROM highscores 
		WHERE game = ? AND id NOT IN (
			SELECT id FROM highscores 
			WHERE game = ? 
			ORDER BY score DESC 
			LIMIT 5
		)
	`, game, game)

	return err
}

func addLocationToDB(lat, lng float64) (bool, error) {
	latRounded := roundCoord(lat, 2)
	lngRounded := roundCoord(lng, 2)

	// Try to insert, will fail if duplicate due to UNIQUE constraint
	result, err := db.Exec(`
		INSERT OR IGNORE INTO locations (lat, lng, lat_rounded, lng_rounded) 
		VALUES (?, ?, ?, ?)
	`, lat, lng, latRounded, lngRounded)
	if err != nil {
		return false, err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}

	return rowsAffected > 0, nil
}

func getLocationsFromDB() ([]Location, error) {
	rows, err := db.Query(`SELECT lat, lng, created_at FROM locations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var locations []Location
	for rows.Next() {
		var loc Location
		if err := rows.Scan(&loc.Lat, &loc.Lng, &loc.Timestamp); err != nil {
			return nil, err
		}
		locations = append(locations, loc)
	}

	return locations, nil
}

func handleAddLocation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var loc Location
	if err := json.NewDecoder(r.Body).Decode(&loc); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Validate coordinates
	if loc.Lat < -90 || loc.Lat > 90 || loc.Lng < -180 || loc.Lng > 180 {
		http.Error(w, "Invalid coordinates", http.StatusBadRequest)
		return
	}

	added, err := addLocationToDB(loc.Lat, loc.Lng)
	if err != nil {
		log.Printf("Error adding location: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"added": added})
}

func handleGetLocations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	locations, err := getLocationsFromDB()
	if err != nil {
		log.Printf("Error getting locations: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if locations == nil {
		locations = []Location{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(locations)
}

func handleGetHighscores(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	game := r.URL.Query().Get("game")
	if game == "" {
		http.Error(w, "Missing game parameter", http.StatusBadRequest)
		return
	}

	// Validate game name
	validGames := map[string]bool{"SNAKE": true, "TETRIS": true, "ASTEROIDS": true, "PONG": true}
	if !validGames[strings.ToUpper(game)] {
		http.Error(w, "Invalid game", http.StatusBadRequest)
		return
	}

	scores, err := getHighscores(strings.ToUpper(game))
	if err != nil {
		log.Printf("Error getting highscores: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(scores)
}

func handleSaveHighscore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Game  string `json:"game"`
		Name  string `json:"name"`
		Score int    `json:"score"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Validate game name
	validGames := map[string]bool{"SNAKE": true, "TETRIS": true, "ASTEROIDS": true, "PONG": true}
	if !validGames[strings.ToUpper(req.Game)] {
		http.Error(w, "Invalid game", http.StatusBadRequest)
		return
	}

	if req.Score < 0 {
		http.Error(w, "Invalid score", http.StatusBadRequest)
		return
	}

	// Cap score at 999999
	score := req.Score
	if score > 999999 {
		score = 999999
	}

	err := saveHighscore(strings.ToUpper(req.Game), req.Name, score)
	if err != nil {
		log.Printf("Error saving highscore: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Return updated scores
	scores, err := getHighscores(strings.ToUpper(req.Game))
	if err != nil {
		log.Printf("Error getting highscores: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(scores)
}

func main() {
	log.Println("Starting CRT Weather Terminal on :8000")

	// Initialize database
	if err := initDB(); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()
	log.Println("Database initialized")

	// API endpoints
	http.HandleFunc("/api/location", handleAddLocation)
	http.HandleFunc("/api/locations", handleGetLocations)
	http.HandleFunc("/api/highscores", handleGetHighscores)
	http.HandleFunc("/api/highscore", handleSaveHighscore)

	// Static files
	http.Handle("/", http.FileServer(http.Dir(".")))

	log.Fatal(http.ListenAndServe(":8000", nil))
}
