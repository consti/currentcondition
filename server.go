package main

import (
	"encoding/json"
	"log"
	"math"
	"net/http"
	"sync"
	"time"
)

// Location represents a visitor's location
type Location struct {
	Lat       float64   `json:"lat"`
	Lng       float64   `json:"lng"`
	Timestamp time.Time `json:"timestamp"`
}

// LocationStore holds unique visitor locations
type LocationStore struct {
	sync.RWMutex
	locations []Location
}

var store = &LocationStore{
	locations: make([]Location, 0),
}

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
	
	added := store.Add(loc.Lat, loc.Lng)
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"added": added})
}

func handleGetLocations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	
	locations := store.GetAll()
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(locations)
}

func main() {
	log.Println("Starting CRT Weather Terminal on :8000")
	
	// API endpoints
	http.HandleFunc("/api/location", handleAddLocation)
	http.HandleFunc("/api/locations", handleGetLocations)
	
	// Static files
	http.Handle("/", http.FileServer(http.Dir(".")))
	
	log.Fatal(http.ListenAndServe(":8000", nil))
}
