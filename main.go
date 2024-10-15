package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"text/template"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

var db *sql.DB

type Config struct {
	Database struct {
		Name string `json:"name"`
	} `json:"database"`
	Server struct {
		Port string `json:"port"`
	} `json:"server"`
	Routes struct {
		Index    string `json:"index"`
		Create   string `json:"create"`
		Redirect string `json:"redirect"`
		Stats    string `json:"stats"`
	} `json:"routes"`
	ShortURL struct {
		Length  int    `json:"length"`
		Charset string `json:"charset"`
	} `json:"shortURL"`
}

var cfg Config

func main() {

	cfgFile, err := os.Open("shorty.config")
	if err != nil {
		log.Fatalf("Failed to open config file: %v", err)
	}
	defer cfgFile.Close()

	bytes, err := io.ReadAll(cfgFile)
	if err != nil {
		log.Fatalf("Failed to read config file: %v", err)
	}

	err = json.Unmarshal(bytes, &cfg)
	if err != nil {
		log.Fatalf("Failed to parse config file: %v", err)
	}

	db, err = sql.Open("sqlite3", cfg.Database.Name)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	_, err = os.Stat(cfg.Database.Name)
	if os.IsNotExist(err) {
		_, err = db.Exec(`CREATE TABLE url_mapping (
			short_url TEXT PRIMARY KEY,
			long_url TEXT NOT NULL,
			visit_count INTEGER DEFAULT 0,
			created_at TEXT DEFAULT CURRENT_TIMESTAMP
		)`)
		if err != nil {
			log.Fatalf("Failed to create table: %v", err)
		}
		fmt.Println("Database initialized.")
	} else {
		// Check if the created_at column exists
		var columnExists bool
		err = db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('url_mapping') WHERE name='created_at'`).Scan(&columnExists)
		if err != nil {
			log.Fatalf("Failed to check for created_at column: %v", err)
		}

		// Add the created_at column if it doesn't exist
		if !columnExists {
			_, err = db.Exec(`ALTER TABLE url_mapping ADD COLUMN created_at TEXT`)
			if err != nil {
				log.Fatalf("Failed to add created_at column: %v", err)
			}
			// Update existing rows with the current timestamp
			_, err = db.Exec(`UPDATE url_mapping SET created_at = CURRENT_TIMESTAMP WHERE created_at IS NULL`)
			if err != nil {
				log.Fatalf("Failed to update existing rows with timestamp: %v", err)
			}
			fmt.Println("Added created_at column to existing database and updated existing rows.")
		}

		var count int
		err = db.QueryRow(`SELECT COUNT(*) FROM url_mapping`).Scan(&count)
		if err != nil {
			log.Fatalf("Failed to query count: %v", err)
		}
		fmt.Printf("Database loaded with %d links.\n", count)
	}

	http.HandleFunc("/", handleIndex)
	http.HandleFunc("/create", handleCreate)
	http.HandleFunc("/r/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/r/")
		if strings.HasSuffix(path, "/stats") {
			shortURL := strings.TrimSuffix(path, "/stats")
			handleLinkStats(w, r, shortURL)
		} else {
			handleRedirect(w, r)
		}
	})
	http.HandleFunc("/stats", handleStats)

	log.Fatal(http.ListenAndServe(cfg.Server.Port, nil))
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	log.Println("Handling index request")
	if r.URL.Path != "/" {
		log.Println("Redirecting to root from:", r.URL.Path)
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	http.ServeFile(w, r, "index.html")
}

func handleCreate(w http.ResponseWriter, r *http.Request) {
	log.Println("Handling create request")
	if r.Method != http.MethodPost {
		log.Println("Not a POST request, redirecting to index")
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
		http.Error(w, "Invalid Content-Type", http.StatusBadRequest)
		return
	}

	longURL := r.FormValue("url")

	_, err := url.ParseRequestURI(longURL)
	if err != nil {
		http.Error(w, "Invalid URL", http.StatusBadRequest)
		return
	}

	if len(longURL) > 2048 {
		http.Error(w, "URL is too long", http.StatusBadRequest)
		return
	}

	shortURL, err := createShortURL(longURL)
	if err != nil {
		log.Printf("Error creating short URL: %v", err)
		http.Error(w, "Failed to create short URL", http.StatusInternalServerError)
		return
	}
	log.Println("Created short URL:", shortURL)

	data := struct {
		ShortURL string
	}{
		ShortURL: shortURL,
	}

	tmpl, err := template.ParseFiles("short.html")
	if err != nil {
		http.Error(w, "Error loading template", http.StatusInternalServerError)
		return
	}

	if err := tmpl.Execute(w, data); err != nil {
		log.Printf("Failed to render template: %v", err)
		http.Error(w, "Error rendering template", http.StatusInternalServerError)
	}
}

func handleRedirect(w http.ResponseWriter, r *http.Request) {
	log.Println("Handling redirect request")
	shortURL := strings.TrimPrefix(r.URL.Path, "/r/")
	log.Printf("Extracted short URL: '%s'", shortURL)

	if shortURL == "" {
		log.Println("Empty short URL, redirecting to root")
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	longURL, err := getLongURL(shortURL)
	if err != nil {
		if err == sql.ErrNoRows {
			log.Printf("No long URL found for short URL '%s'", shortURL)
		} else {
			log.Printf("Error fetching long URL for short URL '%s': %v", shortURL, err)
		}
		log.Println("Redirecting to root due to error")
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	if longURL == "" {
		log.Printf("Empty long URL for short URL '%s'", shortURL)
		log.Println("Redirecting to root due to empty long URL")
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	log.Printf("Found long URL for '%s': '%s'", shortURL, longURL)

	// Update visit count directly in the database
	result, err := db.Exec(`UPDATE url_mapping SET visit_count = visit_count + 1 WHERE short_url = ?`, shortURL)
	if err != nil {
		log.Printf("Error updating visit count for short URL '%s': %v", shortURL, err)
	} else {
		rowsAffected, _ := result.RowsAffected()
		log.Printf("Updated visit count for '%s', rows affected: %d", shortURL, rowsAffected)
	}

	log.Printf("Redirecting to long URL: '%s'", longURL)
	http.Redirect(w, r, longURL, http.StatusFound)
}

func handleStats(w http.ResponseWriter, r *http.Request) {
	log.Println("Handling stats request")

	stats, err := getStats()
	if err != nil {
		log.Printf("Error fetching stats: %v", err)
		http.Error(w, "Error fetching stats", http.StatusInternalServerError)
		return
	}

	tmpl, err := template.ParseFiles("stats.html")
	if err != nil {
		log.Printf("Error parsing stats template: %v", err)
		http.Error(w, "Error loading template", http.StatusInternalServerError)
		return
	}

	if err := tmpl.Execute(w, stats); err != nil {
		log.Printf("Error executing stats template: %v", err)
		http.Error(w, "Error rendering template", http.StatusInternalServerError)
	}
}

func createShortURL(longURL string) (string, error) {
	// First, check if the long URL already exists
	var existingShortURL string
	err := db.QueryRow(`SELECT short_url FROM url_mapping WHERE long_url = ? ORDER BY rowid ASC LIMIT 1`, longURL).Scan(&existingShortURL)
	if err == nil {
		// If we found an existing short URL, return it
		log.Printf("Found existing short URL '%s' for long URL '%s'", existingShortURL, longURL)
		return existingShortURL, nil
	} else if err != sql.ErrNoRows {
		// If there was an error other than "no rows", return it
		log.Printf("Error checking for existing long URL: %v", err)
		return "", err
	}

	// If we didn't find an existing short URL, create a new one
	for {
		shortURL := randomString(cfg.ShortURL.Length)
		log.Printf("Generated random short URL: '%s'", shortURL)
		exists, err := shortURLExists(shortURL)
		if err != nil {
			log.Printf("Error checking if short URL exists: %v", err)
			return "", err
		}
		if !exists {
			_, err := db.Exec(`INSERT INTO url_mapping (short_url, long_url, created_at) VALUES (?, ?, datetime('now'))`, shortURL, longURL)
			if err != nil {
				log.Printf("Error inserting short URL '%s' into DB: %v", shortURL, err)
				return "", err
			}
			log.Printf("Successfully saved short URL to DB: '%s' -> '%s'", shortURL, longURL)
			return shortURL, nil
		}
	}
}

func getLongURL(shortURL string) (string, error) {
	var longURL string
	err := db.QueryRow(`SELECT long_url FROM url_mapping WHERE short_url = ?`, shortURL).Scan(&longURL)
	if err != nil {
		if err == sql.ErrNoRows {
			log.Printf("No long URL found in DB for short URL '%s'", shortURL)
		} else {
			log.Printf("Error querying DB for short URL '%s': %v", shortURL, err)
		}
		return "", err
	}
	log.Printf("Fetched long URL from DB for '%s': '%s'", shortURL, longURL)
	return longURL, nil
}

func shortURLExists(shortURL string) (bool, error) {
	var exists bool
	err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM url_mapping WHERE short_url=?)`, shortURL).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}

func randomString(length int) string {
	b := make([]byte, length)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = cfg.ShortURL.Charset[b[i]%byte(len(cfg.ShortURL.Charset))]
	}
	return string(b)
}

// Add these new types to support the stats
type LinkStats struct {
	ShortURL   string
	LongURL    string
	VisitCount int
	CreatedAt  time.Time
}

func (l LinkStats) FormattedCreatedAt() string {
	return l.CreatedAt.Format("2006-01-02 15:04:05")
}

type Stats struct {
	TotalLinks       int
	TotalClicks      int
	ClicksToday      int
	PopularLinks     []LinkStats
	RecentLinks      []LinkStats
	MostClickedLinks []LinkStats
}

// Add the getStats function
func getStats() (Stats, error) {
	var stats Stats
	var err error

	// Get total links
	err = db.QueryRow("SELECT COUNT(*) FROM url_mapping").Scan(&stats.TotalLinks)
	if err != nil {
		return stats, err
	}

	// Get total clicks
	err = db.QueryRow("SELECT COALESCE(SUM(visit_count), 0) FROM url_mapping").Scan(&stats.TotalClicks)
	if err != nil {
		return stats, err
	}

	// Get clicks today
	today := time.Now().Format("2006-01-02")
	err = db.QueryRow("SELECT COALESCE(SUM(visit_count), 0) FROM url_mapping WHERE DATE(created_at) = ?", today).Scan(&stats.ClicksToday)
	if err != nil {
		return stats, err
	}

	// Get all links, ordered by visit count
	rows, err := db.Query("SELECT short_url, long_url, visit_count, created_at FROM url_mapping ORDER BY visit_count DESC")
	if err != nil {
		return stats, err
	}
	defer rows.Close()

	var allLinks []LinkStats
	for rows.Next() {
		var link LinkStats
		var createdAtStr string
		err := rows.Scan(&link.ShortURL, &link.LongURL, &link.VisitCount, &createdAtStr)
		if err != nil {
			return stats, err
		}
		link.CreatedAt, err = time.Parse("2006-01-02 15:04:05", createdAtStr)
		if err != nil {
			return stats, fmt.Errorf("error parsing created_at time: %v", err)
		}
		allLinks = append(allLinks, link)
	}

	// Populate stats
	stats.PopularLinks = allLinks[:min(10, len(allLinks))]
	stats.MostClickedLinks = allLinks[:min(10, len(allLinks))]

	// Sort by creation time for recent links
	sort.Slice(allLinks, func(i, j int) bool {
		return allLinks[i].CreatedAt.After(allLinks[j].CreatedAt)
	})
	stats.RecentLinks = allLinks[:min(10, len(allLinks))]

	return stats, nil
}

// Helper function for slicing
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Add this new function to handle individual link stats
func handleLinkStats(w http.ResponseWriter, r *http.Request, shortURL string) {
	log.Printf("Handling stats request for short URL: %s", shortURL)

	linkStats, err := getLinkStats(shortURL)
	if err != nil {
		log.Printf("Error fetching stats for short URL %s: %v", shortURL, err)
		http.Error(w, "Error fetching link stats", http.StatusInternalServerError)
		return
	}

	tmpl, err := template.ParseFiles("link_stats.html")
	if err != nil {
		log.Printf("Error parsing link stats template: %v", err)
		http.Error(w, "Error loading template", http.StatusInternalServerError)
		return
	}

	if err := tmpl.Execute(w, linkStats); err != nil {
		log.Printf("Error executing link stats template: %v", err)
		http.Error(w, "Error rendering template", http.StatusInternalServerError)
	}
}

// Add this new function to fetch stats for a specific link
func getLinkStats(shortURL string) (LinkStats, error) {
	var stats LinkStats
	var createdAtStr string

	err := db.QueryRow(`
		SELECT short_url, long_url, visit_count, created_at 
		FROM url_mapping 
		WHERE short_url = ?
	`, shortURL).Scan(&stats.ShortURL, &stats.LongURL, &stats.VisitCount, &createdAtStr)

	if err != nil {
		return stats, err
	}

	stats.CreatedAt, err = time.Parse("2006-01-02 15:04:05", createdAtStr)
	if err != nil {
		return stats, fmt.Errorf("error parsing created_at time: %v", err)
	}

	return stats, nil
}
