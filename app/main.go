package main

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"sync"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"
	_ "modernc.org/sqlite"
)

type Message struct {
	From    string `json:"from"`
	Content string `json:"content"`
}

var (
	peers []string
	mu    sync.Mutex
)

const reset = "\033[0m"

var colorMap = map[string]string{
	"k": "\033[90m", // hell-schwarz (grau)
	"r": "\033[91m", // rot
	"g": "\033[92m", // grün
	"y": "\033[93m", // gelb
	"b": "\033[94m", // blau
	"m": "\033[95m", // magenta
	"c": "\033[96m", // cyan
	"w": "\033[97m", // weiß
}

// cp: farbige Formatierung wie fmt.Sprint – nur mit Farbe per Kürzel.
func cp(colorKey string, parts ...interface{}) string {
	code, ok := colorMap[colorKey]
	if !ok {
		return fmt.Sprint(parts...)
	}
	return code + fmt.Sprint(parts...) + reset
}

/* ===========================
   Datenbank / Auth
=========================== */

func initDB() *sql.DB {
	db, err := sql.Open("sqlite", "./users.db")
	if err != nil {
		log.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS users(
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT UNIQUE NOT NULL,
			password TEXT NOT NULL
		);
	`); err != nil {
		log.Fatal(err)
	}
	return db
}

func registerUser(db *sql.DB, username, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO users(username,password) VALUES(?,?)`, username, string(hash))
	return err
}

func loginUser(db *sql.DB, username, password string) bool {
	var stored string
	err := db.QueryRow(`SELECT password FROM users WHERE username = ?`, username).Scan(&stored)
	if err != nil {
		fmt.Println(cp("r", "❌ Benutzer nicht gefunden"))
		return false
	}
	if err := bcrypt.CompareHashAndPassword([]byte(stored), []byte(password)); err != nil {
		fmt.Println(cp("r", "❌ Falsches Passwort"))
		return false
	}
	return true
}

func promptHidden(label string) string {
	fmt.Print(label)
	pw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(pw))
}

/* ===========================
   Netzwerk
=========================== */

func startServer(port string) {
	ln, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Fatal(err)
	}
	defer ln.Close()
	fmt.Println(cp("g", "Listening on port ", port))

	for {
		conn, err := ln.Accept()
		if err != nil {
			fmt.Println(cp("r", "Connection error: ", err))
			continue
		}
		go handleConnection(conn)
	}
}

func handleConnection(conn net.Conn) {
	defer conn.Close()
	dec := json.NewDecoder(conn)
	var msg Message
	if err := dec.Decode(&msg); err == nil {
		// Eigene Nachrichten blau, fremde grün → hier einfach grün für Empfang
		fmt.Printf("%s[%s]%s %s\n", colorMap["g"], msg.From, reset, msg.Content)
	}
}

func broadcast(msg Message) {
	mu.Lock()
	targets := append([]string(nil), peers...)
	mu.Unlock()

	for _, addr := range targets {
		go func(a string) {
			conn, err := net.Dial("tcp", a)
			if err != nil {
				fmt.Println(cp("r", "Failed to connect to ", a))
				return
			}
			defer conn.Close()
			enc := json.NewEncoder(conn)
			_ = enc.Encode(msg)
		}(addr)
	}
}

/* ===========================
   Main / CLI
=========================== */

func main() {
	fmt.Println(cp("g", "Welcome to the PostScriptum P2P Messenger!"))
	if len(os.Args) < 2 {
		fmt.Println("Usage: go run . <port>")
		return
	}
	port := os.Args[1]

	// DB & Auth
	db := initDB()
	defer db.Close()

	reader := bufio.NewReader(os.Stdin)

	var username string
	for {
		fmt.Print("Login (l) oder Registrieren (r)? ")
		choice, _ := reader.ReadString('\n')
		choice = strings.TrimSpace(strings.ToLower(choice))

		fmt.Print("Username: ")
		un, _ := reader.ReadString('\n')
		un = strings.TrimSpace(un)

		pw := promptHidden("Passwort: ")

		switch choice {
		case "r":
			if err := registerUser(db, un, pw); err != nil {
				fmt.Println(cp("r", "Registrierung fehlgeschlagen: ", err))
			} else {
				fmt.Println(cp("g", "✅ Benutzer registriert. Bitte einloggen."))
			}
			continue
		case "l":
			if loginUser(db, un, pw) {
				username = un
				fmt.Println(cp("g", "✅ Login erfolgreich!"))
				goto START_CHAT
			}
		default:
			fmt.Println(cp("y", "Bitte 'l' oder 'r' eingeben."))
		}
	}

START_CHAT:
	// Server starten
	go startServer(port)

	// Hilfe anzeigen
	fmt.Println(cp("c", "Commands:"))
	fmt.Println(cp("c", "  /connect <ip:port>   - Peer hinzufügen"))
	fmt.Println(cp("c", "  /peers               - Peerliste anzeigen"))
	fmt.Println(cp("c", "  /quit                - Beenden"))

	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())

		switch {
		case line == "/quit":
			fmt.Println(cp("y", "Bye."))
			return

		case line == "/peers":
			mu.Lock()
			if len(peers) == 0 {
				fmt.Println(cp("y", "(keine Peers)"))
			} else {
				for _, p := range peers {
					fmt.Println(cp("w", "• ", p))
				}
			}
			mu.Unlock()
			continue

		case strings.HasPrefix(line, "/connect "):
			peer := strings.TrimSpace(strings.TrimPrefix(line, "/connect "))
			if peer == "" {
				fmt.Println(cp("y", "Usage: /connect <ip:port>"))
				continue
			}
			mu.Lock()
			peers = append(peers, peer)
			mu.Unlock()
			fmt.Println(cp("g", "Connected to ", peer))
			continue
		}

		if line == "" {
			continue
		}

		// Eigene Nachrichten blau anzeigen
		fmt.Printf("%s[%s]%s %s\n", colorMap["b"], username, reset, line)

		msg := Message{From: username, Content: line}
		broadcast(msg)
	}

	if err := sc.Err(); err != nil {
		fmt.Println(cp("r", "Input error: ", err))
	}
}
