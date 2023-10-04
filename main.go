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
	"strings"
	"text/template"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

var db *sql.DB
var visitCountCache = make(map[string]int)

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
			visit_count INTEGER DEFAULT 0
		)`)
		if err != nil {
			log.Fatalf("Failed to create table: %v", err)
		}
		fmt.Println("Database initialized.")
	} else {
		var count int
		err = db.QueryRow(`SELECT COUNT(*) FROM url_mapping`).Scan(&count)
		if err != nil {
			log.Fatalf("Failed to query count: %v", err)
		}
		fmt.Printf("Database loaded with %d links.\n", count)
	}

	ticker := time.NewTicker(1 * time.Minute)
	go func() {
		for range ticker.C {
			writeCacheToDB()
		}
	}()

	http.HandleFunc(cfg.Routes.Index, handleIndex)
	http.HandleFunc(cfg.Routes.Create, handleCreate)
	http.HandleFunc(cfg.Routes.Redirect, handleRedirect)

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
	shortURL := strings.TrimPrefix(r.URL.Path, "/#/")
	log.Println("Extracted short URL:", shortURL)

	longURL, err := getLongURL(shortURL)
	if err != nil {
		log.Printf("Error fetching long URL for short URL %s: %v", shortURL, err)
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	log.Println("Redirecting to long URL:", longURL)

	visitCountCache[shortURL]++

	http.Redirect(w, r, longURL, http.StatusFound)
}

func createShortURL(longURL string) (string, error) {
	for {
		shortURL := randomString(8)
		log.Println("Generated random short URL:", shortURL)
		exists, err := shortURLExists(shortURL)
		if err != nil {
			return "", err
		}
		if !exists {
			_, err := db.Exec(`INSERT INTO url_mapping (short_url, long_url) VALUES (?, ?)`, shortURL, longURL)
			if err != nil {
				log.Printf("Error inserting short URL %s into DB: %v", shortURL, err)
				return "", err
			}
			log.Println("Successfully saved short URL to DB:", shortURL)
			return shortURL, nil
		}
	}
}

func writeCacheToDB() {
	log.Println("Writing cache to DB")
	for shortURL, count := range visitCountCache {
		log.Printf("Updating visit count for short URL %s by %d", shortURL, count)

		_, err := db.Exec(`UPDATE url_mapping SET visit_count = visit_count + ? WHERE short_url = ?`, count, shortURL)
		if err != nil {
			log.Printf("Failed to update visit count: %v", err)
			continue
		}

		visitCountCache[shortURL] = 0
	}
}

func getLongURL(shortURL string) (string, error) {
	var longURL string
	err := db.QueryRow(`SELECT long_url FROM url_mapping WHERE short_url = ?`, shortURL).Scan(&longURL)
	if err != nil {
		log.Printf("Error querying DB for short URL %s: %v", shortURL, err)
		return "", err
	}
	log.Println("Fetched long URL from DB:", longURL)
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
