package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
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

// LocationResponse includes visitor count info
type LocationResponse struct {
	Added        bool `json:"added"`
	IsFirst      bool `json:"isFirst"`
	VisitorCount int  `json:"visitorCount"`
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

	// Create locations table with visitor count
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS locations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			lat REAL NOT NULL,
			lng REAL NOT NULL,
			lat_rounded REAL NOT NULL,
			lng_rounded REAL NOT NULL,
			visitor_count INTEGER DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(lat_rounded, lng_rounded)
		);
	`)
	if err != nil {
		return err
	}

	// Add visitor_count column if it doesn't exist (migration for existing DBs)
	_, _ = db.Exec(`ALTER TABLE locations ADD COLUMN visitor_count INTEGER DEFAULT 1`)

	// Create visitors table to track unique visitors by cookie
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS visitors (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			visitor_id TEXT UNIQUE NOT NULL,
			lat_rounded REAL,
			lng_rounded REAL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
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

// generateVisitorID creates a random visitor ID
func generateVisitorID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// checkVisitorExists checks if a visitor ID already exists and has a location
func checkVisitorExists(visitorID string) (bool, float64, float64, error) {
	var latRounded, lngRounded sql.NullFloat64
	err := db.QueryRow(`SELECT lat_rounded, lng_rounded FROM visitors WHERE visitor_id = ?`, visitorID).Scan(&latRounded, &lngRounded)
	if err == sql.ErrNoRows {
		return false, 0, 0, nil
	}
	if err != nil {
		return false, 0, 0, err
	}
	return true, latRounded.Float64, lngRounded.Float64, nil
}

// addOrUpdateVisitor adds a new visitor or updates existing one
func addOrUpdateVisitor(visitorID string, latRounded, lngRounded float64) error {
	_, err := db.Exec(`
		INSERT INTO visitors (visitor_id, lat_rounded, lng_rounded) 
		VALUES (?, ?, ?)
		ON CONFLICT(visitor_id) DO UPDATE SET lat_rounded = ?, lng_rounded = ?
	`, visitorID, latRounded, lngRounded, latRounded, lngRounded)
	return err
}

func addLocationToDB(lat, lng float64, visitorID string) (LocationResponse, error) {
	latRounded := roundCoord(lat, 2)
	lngRounded := roundCoord(lng, 2)
	response := LocationResponse{}

	// Check if this visitor already registered a location
	exists, oldLat, oldLng, err := checkVisitorExists(visitorID)
	if err != nil {
		return response, err
	}

	// If visitor exists and already has the same location, don't count again
	if exists && oldLat == latRounded && oldLng == lngRounded {
		// Just return current count for this location
		var count int
		err = db.QueryRow(`SELECT visitor_count FROM locations WHERE lat_rounded = ? AND lng_rounded = ?`, latRounded, lngRounded).Scan(&count)
		if err != nil && err != sql.ErrNoRows {
			return response, err
		}
		response.Added = false
		response.IsFirst = false
		response.VisitorCount = count
		return response, nil
	}

	// Try to insert new location
	result, err := db.Exec(`
		INSERT OR IGNORE INTO locations (lat, lng, lat_rounded, lng_rounded, visitor_count) 
		VALUES (?, ?, ?, ?, 1)
	`, lat, lng, latRounded, lngRounded)
	if err != nil {
		return response, err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return response, err
	}

	if rowsAffected > 0 {
		// New location - this visitor is the first from here
		response.Added = true
		response.IsFirst = true
		response.VisitorCount = 1
	} else {
		// Location exists - increment visitor count
		_, err = db.Exec(`UPDATE locations SET visitor_count = visitor_count + 1 WHERE lat_rounded = ? AND lng_rounded = ?`, latRounded, lngRounded)
		if err != nil {
			return response, err
		}

		// Get updated count
		var count int
		err = db.QueryRow(`SELECT visitor_count FROM locations WHERE lat_rounded = ? AND lng_rounded = ?`, latRounded, lngRounded).Scan(&count)
		if err != nil {
			return response, err
		}

		response.Added = false
		response.IsFirst = false
		response.VisitorCount = count
	}

	// Record this visitor
	err = addOrUpdateVisitor(visitorID, latRounded, lngRounded)
	if err != nil {
		return response, err
	}

	return response, nil
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

	// Get or create visitor ID from cookie
	visitorID := ""
	cookie, err := r.Cookie("visitor_id")
	if err == nil {
		visitorID = cookie.Value
	} else {
		visitorID = generateVisitorID()
	}

	// Set cookie (valid for 1 year)
	http.SetCookie(w, &http.Cookie{
		Name:     "visitor_id",
		Value:    visitorID,
		Path:     "/",
		MaxAge:   365 * 24 * 60 * 60,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	response, err := addLocationToDB(loc.Lat, loc.Lng, visitorID)
	if err != nil {
		log.Printf("Error adding location: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
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
