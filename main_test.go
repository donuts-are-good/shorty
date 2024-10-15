package main

import (
	"database/sql"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestMain(m *testing.M) {
	// Disable logging during tests
	log.SetOutput(os.NewFile(0, os.DevNull))
	os.Exit(m.Run())
}

func TestCreateShortURL(t *testing.T) {
	// Create a mock database
	mockDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("An error '%s' was not expected when opening a stub database connection", err)
	}
	defer mockDB.Close()

	// Replace the global db with our mock database
	db = mockDB

	// Set up the configuration for testing
	cfg = Config{
		ShortURL: struct {
			Length  int    `json:"length"`
			Charset string `json:"charset"`
		}{
			Length:  6,
			Charset: "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789",
		},
	}

	t.Run("Existing URL", func(t *testing.T) {
		longURL := "https://example.com"
		expectedShortURL := "abc123"

		mock.ExpectQuery("SELECT short_url FROM url_mapping WHERE long_url").
			WithArgs(longURL).
			WillReturnRows(sqlmock.NewRows([]string{"short_url"}).AddRow(expectedShortURL))

		shortURL, err := createShortURL(longURL)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if shortURL != expectedShortURL {
			t.Errorf("Expected short URL %s, got %s", expectedShortURL, shortURL)
		}
	})

	t.Run("New URL", func(t *testing.T) {
		longURL := "https://newexample.com"

		mock.ExpectQuery("SELECT short_url FROM url_mapping WHERE long_url").
			WithArgs(longURL).
			WillReturnError(sql.ErrNoRows)

		mock.ExpectQuery("SELECT EXISTS").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

		mock.ExpectExec("INSERT INTO url_mapping").
			WithArgs(sqlmock.AnyArg(), longURL).
			WillReturnResult(sqlmock.NewResult(1, 1))

		shortURL, err := createShortURL(longURL)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if len(shortURL) != cfg.ShortURL.Length {
			t.Errorf("Expected short URL length %d, got %d", cfg.ShortURL.Length, len(shortURL))
		}
	})

	t.Run("Database error", func(t *testing.T) {
		longURL := "https://errorexample.com"

		mock.ExpectQuery("SELECT short_url FROM url_mapping WHERE long_url").
			WithArgs(longURL).
			WillReturnError(sql.ErrConnDone)

		_, err := createShortURL(longURL)
		if err == nil {
			t.Error("Expected an error, got nil")
		}
	})

	// Ensure all expectations were met
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("There were unfulfilled expectations: %s", err)
	}
}

func TestHandleRedirect(t *testing.T) {
	// Create a mock database
	mockDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("An error '%s' was not expected when opening a stub database connection", err)
	}
	defer mockDB.Close()

	// Replace the global db with our mock database
	db = mockDB

	t.Run("Successful Redirect", func(t *testing.T) {
		shortURL := "abc123"
		longURL := "https://example.com"

		mock.ExpectQuery("SELECT long_url FROM url_mapping WHERE short_url").
			WithArgs(shortURL).
			WillReturnRows(sqlmock.NewRows([]string{"long_url"}).AddRow(longURL))

		mock.ExpectExec("UPDATE url_mapping SET visit_count").
			WithArgs(shortURL).
			WillReturnResult(sqlmock.NewResult(1, 1))

		req, err := http.NewRequest("GET", "/r/"+shortURL, nil)
		if err != nil {
			t.Fatal(err)
		}

		rr := httptest.NewRecorder()
		handler := http.HandlerFunc(handleRedirect)

		handler.ServeHTTP(rr, req)

		if status := rr.Code; status != http.StatusFound {
			t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusFound)
		}

		if location := rr.Header().Get("Location"); location != longURL {
			t.Errorf("handler returned wrong redirect location: got %v want %v", location, longURL)
		}
	})

	t.Run("Non-existent Short URL", func(t *testing.T) {
		shortURL := "nonexistent"

		mock.ExpectQuery("SELECT long_url FROM url_mapping WHERE short_url").
			WithArgs(shortURL).
			WillReturnError(sql.ErrNoRows)

		req, err := http.NewRequest("GET", "/r/"+shortURL, nil)
		if err != nil {
			t.Fatal(err)
		}

		rr := httptest.NewRecorder()
		handler := http.HandlerFunc(handleRedirect)

		handler.ServeHTTP(rr, req)

		if status := rr.Code; status != http.StatusFound {
			t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusFound)
		}

		if location := rr.Header().Get("Location"); location != "/" {
			t.Errorf("handler returned wrong redirect location: got %v want /", location)
		}
	})
}

func TestRandomString(t *testing.T) {
	cfg = Config{
		ShortURL: struct {
			Length  int    `json:"length"`
			Charset string `json:"charset"`
		}{
			Length:  6,
			Charset: "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789",
		},
	}

	t.Run("Correct Length", func(t *testing.T) {
		result := randomString(cfg.ShortURL.Length)
		if len(result) != cfg.ShortURL.Length {
			t.Errorf("randomString returned wrong length: got %v want %v", len(result), cfg.ShortURL.Length)
		}
	})

	t.Run("Characters from Charset", func(t *testing.T) {
		result := randomString(cfg.ShortURL.Length)
		for _, char := range result {
			if !strings.ContainsRune(cfg.ShortURL.Charset, char) {
				t.Errorf("randomString returned character not in charset: %c", char)
			}
		}
	})

	t.Run("Randomness", func(t *testing.T) {
		results := make(map[string]bool)
		for i := 0; i < 1000; i++ {
			result := randomString(cfg.ShortURL.Length)
			results[result] = true
		}
		if len(results) < 900 {
			t.Errorf("randomString does not appear to be sufficiently random")
		}
	})
}

func TestHandleCreate(t *testing.T) {
	// Create a mock database
	mockDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("An error '%s' was not expected when opening a stub database connection", err)
	}
	defer mockDB.Close()

	// Replace the global db with our mock database
	db = mockDB

	t.Run("Valid URL", func(t *testing.T) {
		longURL := "https://example.com"
		shortURL := "abc123"

		mock.ExpectQuery("SELECT short_url FROM url_mapping WHERE long_url").
			WithArgs(longURL).
			WillReturnRows(sqlmock.NewRows([]string{"short_url"}).AddRow(shortURL))

		req, err := http.NewRequest("POST", "/create", strings.NewReader("url="+longURL))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		rr := httptest.NewRecorder()
		handler := http.HandlerFunc(handleCreate)

		handler.ServeHTTP(rr, req)

		if status := rr.Code; status != http.StatusOK {
			t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
		}

		expected := shortURL
		if !strings.Contains(rr.Body.String(), expected) {
			t.Errorf("handler returned unexpected body: got %v want %v", rr.Body.String(), expected)
		}
	})

	t.Run("Invalid URL", func(t *testing.T) {
		longURL := "not-a-valid-url"

		req, err := http.NewRequest("POST", "/create", strings.NewReader("url="+longURL))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		rr := httptest.NewRecorder()
		handler := http.HandlerFunc(handleCreate)

		handler.ServeHTTP(rr, req)

		if status := rr.Code; status != http.StatusBadRequest {
			t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
		}
	})

	t.Run("URL Too Long", func(t *testing.T) {
		longURL := "https://example.com/" + strings.Repeat("a", 2048)

		req, err := http.NewRequest("POST", "/create", strings.NewReader("url="+longURL))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		rr := httptest.NewRecorder()
		handler := http.HandlerFunc(handleCreate)

		handler.ServeHTTP(rr, req)

		if status := rr.Code; status != http.StatusBadRequest {
			t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
		}
	})
}

func TestGetLongURL(t *testing.T) {
	// Create a mock database
	mockDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("An error '%s' was not expected when opening a stub database connection", err)
	}
	defer mockDB.Close()

	// Replace the global db with our mock database
	db = mockDB

	t.Run("Existing Short URL", func(t *testing.T) {
		shortURL := "abc123"
		expectedLongURL := "https://example.com"

		mock.ExpectQuery("SELECT long_url FROM url_mapping WHERE short_url").
			WithArgs(shortURL).
			WillReturnRows(sqlmock.NewRows([]string{"long_url"}).AddRow(expectedLongURL))

		longURL, err := getLongURL(shortURL)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if longURL != expectedLongURL {
			t.Errorf("getLongURL returned wrong URL: got %v want %v", longURL, expectedLongURL)
		}
	})

	t.Run("Non-existent Short URL", func(t *testing.T) {
		shortURL := "nonexistent"

		mock.ExpectQuery("SELECT long_url FROM url_mapping WHERE short_url").
			WithArgs(shortURL).
			WillReturnError(sql.ErrNoRows)

		_, err := getLongURL(shortURL)
		if err == nil {
			t.Error("Expected an error, got nil")
		}
	})
}

func TestHandleIndex(t *testing.T) {
	req, err := http.NewRequest("GET", "/", nil)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(handleIndex)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handleIndex returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	// Test redirection for non-root paths
	req, err = http.NewRequest("GET", "/invalid-path", nil)
	if err != nil {
		t.Fatal(err)
	}

	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusFound {
		t.Errorf("handleIndex returned wrong status code for invalid path: got %v want %v", status, http.StatusFound)
	}
}

func TestHandleStats(t *testing.T) {
	// Create a mock database
	mockDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("An error '%s' was not expected when opening a stub database connection", err)
	}
	defer mockDB.Close()

	// Replace the global db with our mock database
	db = mockDB

	mock.ExpectQuery("SELECT COUNT.*FROM url_mapping").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(10))
	mock.ExpectQuery("SELECT SUM.*FROM url_mapping").WillReturnRows(sqlmock.NewRows([]string{"sum"}).AddRow(100))
	mock.ExpectQuery("SELECT COALESCE.*FROM url_mapping").WillReturnRows(sqlmock.NewRows([]string{"sum"}).AddRow(5))
	mock.ExpectQuery("SELECT short_url, long_url, visit_count, created_at FROM url_mapping").
		WillReturnRows(sqlmock.NewRows([]string{"short_url", "long_url", "visit_count", "created_at"}).
			AddRow("abc123", "https://example.com", 50, time.Now()))

	req, err := http.NewRequest("GET", "/stats", nil)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(handleStats)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handleStats returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	// You might want to add more assertions here to check the content of the response
}

func TestGetStats(t *testing.T) {
	// Create a mock database
	mockDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("An error '%s' was not expected when opening a stub database connection", err)
	}
	defer mockDB.Close()

	// Replace the global db with our mock database
	db = mockDB

	mock.ExpectQuery("SELECT COUNT.*FROM url_mapping").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(10))
	mock.ExpectQuery("SELECT SUM.*FROM url_mapping").WillReturnRows(sqlmock.NewRows([]string{"sum"}).AddRow(100))
	mock.ExpectQuery("SELECT COALESCE.*FROM url_mapping").WillReturnRows(sqlmock.NewRows([]string{"sum"}).AddRow(5))
	mock.ExpectQuery("SELECT short_url, long_url, visit_count, created_at FROM url_mapping").
		WillReturnRows(sqlmock.NewRows([]string{"short_url", "long_url", "visit_count", "created_at"}).
			AddRow("abc123", "https://example.com", 50, time.Now()).
			AddRow("def456", "https://example.org", 30, time.Now()))

	stats, err := getStats()
	if err != nil {
		t.Fatalf("getStats returned an error: %v", err)
	}

	if stats.TotalLinks != 10 {
		t.Errorf("getStats returned wrong TotalLinks: got %v want %v", stats.TotalLinks, 10)
	}

	if stats.TotalClicks != 100 {
		t.Errorf("getStats returned wrong TotalClicks: got %v want %v", stats.TotalClicks, 100)
	}

	if stats.ClicksToday != 5 {
		t.Errorf("getStats returned wrong ClicksToday: got %v want %v", stats.ClicksToday, 5)
	}

	if len(stats.PopularLinks) != 2 {
		t.Errorf("getStats returned wrong number of PopularLinks: got %v want %v", len(stats.PopularLinks), 2)
	}
}

func TestShortURLExists(t *testing.T) {
	// Create a mock database
	mockDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("An error '%s' was not expected when opening a stub database connection", err)
	}
	defer mockDB.Close()

	// Replace the global db with our mock database
	db = mockDB

	t.Run("Existing Short URL", func(t *testing.T) {
		shortURL := "abc123"

		mock.ExpectQuery("SELECT EXISTS").
			WithArgs(shortURL).
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))

		exists, err := shortURLExists(shortURL)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if !exists {
			t.Errorf("shortURLExists returned false for existing URL")
		}
	})

	t.Run("Non-existent Short URL", func(t *testing.T) {
		shortURL := "nonexistent"

		mock.ExpectQuery("SELECT EXISTS").
			WithArgs(shortURL).
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

		exists, err := shortURLExists(shortURL)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if exists {
			t.Errorf("shortURLExists returned true for non-existent URL")
		}
	})
}

// Add more edge cases to existing tests
func TestCreateShortURLEdgeCases(t *testing.T) {
	// Create a mock database
	mockDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("An error '%s' was not expected when opening a stub database connection", err)
	}
	defer mockDB.Close()

	// Replace the global db with our mock database
	db = mockDB

	// Set up the configuration for testing
	cfg = Config{
		ShortURL: struct {
			Length  int    `json:"length"`
			Charset string `json:"charset"`
		}{
			Length:  6,
			Charset: "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789",
		},
	}

	t.Run("Very Long URL", func(t *testing.T) {
		longURL := "https://example.com/" + strings.Repeat("a", 2000)

		mock.ExpectQuery("SELECT short_url FROM url_mapping WHERE long_url").
			WithArgs(longURL).
			WillReturnError(sql.ErrNoRows)

		mock.ExpectQuery("SELECT EXISTS").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

		mock.ExpectExec("INSERT INTO url_mapping").
			WithArgs(sqlmock.AnyArg(), longURL).
			WillReturnResult(sqlmock.NewResult(1, 1))

		shortURL, err := createShortURL(longURL)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if len(shortURL) != cfg.ShortURL.Length {
			t.Errorf("Expected short URL length %d, got %d", cfg.ShortURL.Length, len(shortURL))
		}
	})

	// Ensure all expectations were met
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("There were unfulfilled expectations: %s", err)
	}

	// ... (add more edge cases as needed)
}

// Test the cache writing mechanism
func TestWriteCacheToDB(t *testing.T) {
	// Create a mock database
	mockDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("An error '%s' was not expected when opening a stub database connection", err)
	}
	defer mockDB.Close()

	// Replace the global db with our mock database
	db = mockDB

	// Set up the visitCountCache
	visitCountCache = map[string]int{
		"abc123": 5,
		"def456": 10,
		"ghi789": 0, // This should not be updated
	}

	mock.ExpectExec("UPDATE url_mapping SET visit_count = visit_count \\+ \\? WHERE short_url = \\?").
		WithArgs(5, "abc123").
		WillReturnResult(sqlmock.NewResult(0, 1))

	mock.ExpectExec("UPDATE url_mapping SET visit_count = visit_count \\+ \\? WHERE short_url = \\?").
		WithArgs(10, "def456").
		WillReturnResult(sqlmock.NewResult(0, 1))

	writeCacheToDB()

	// Check if all expectations were met
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("There were unfulfilled expectations: %s", err)
	}

	// Check if the cache was cleared properly
	for shortURL, count := range visitCountCache {
		if count != 0 {
			t.Errorf("Cache was not cleared properly for %s: expected 0, got %d", shortURL, count)
		}
	}
}
