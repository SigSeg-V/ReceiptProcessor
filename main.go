package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/google/uuid"
)

// map in lieu of real DB
type DB struct {
	Db   map[string]int
	Lock sync.RWMutex
}

var db DB
var logger *log.Logger = log.New(os.Stdout, "receipt-processor > ", 0)

func NewDB() DB {
	return DB{Db: make(map[string]int), Lock: sync.RWMutex{}}
}

func (db *DB) Get(id string) int {
	db.Lock.RLock()
	defer db.Lock.RUnlock()

	return db.Db[id]
}

func (db *DB) Put(id string, points int) {
	db.Lock.Lock()
	defer db.Lock.Unlock()

	db.Db[id] = points
}

type Receipt struct {
	Retailer     string `json:"retailer"`
	PurchaseDate string `json:"purchaseDate"`
	PurchaseTime string `json:"purchaseTime"`
	Items        []Item `json:"items"`
	Total        string `json:"total"`
}

type Item struct {
	ShortDescription string `json:"shortDescription"`
	Price            string `json:"price"`
}

type ProcessResponse struct {
	Id uuid.UUID `json:"id"`
}

type PointsResponse struct {
	Points int `json:"points"`
}

func main() {
	db = NewDB()

	err := serve()
	if err == http.ErrServerClosed {
		logger.Print("server closed")
	} else if err != nil {
		logger.Printf(err.Error())
		os.Exit(1)
	}
}

func getPoints(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "The receipt is invalid", http.StatusBadRequest)
		return
	}

	points := db.Get(id)

	response, err := json.Marshal(PointsResponse{Points: points})
	if err != nil {
		http.Error(w, "no receipt found for that id", http.StatusNotFound)
		return
	}

	logger.Printf("got request in get points\npoints: %d\n", points)
	io.WriteString(w, string(response))
}

func postProcessReceipt(w http.ResponseWriter, r *http.Request) {
	// denying unwanted requests
	if r.Method != "POST" {
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return
	}

	// unmarshall the request and send the points awarded to db
	payload := &Receipt{}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "The receipt is invalid", http.StatusBadRequest)
		return
	}

	json.Unmarshal(body, payload)

	points, err := countPoints(payload)
	if err != nil {
		http.Error(w, "The receipt is invalid", http.StatusBadRequest)
		return
	}

	// generate uuid for the points
	id := uuid.New()
	db.Put(id.String(), points)

	response, err := json.Marshal(ProcessResponse{Id: id})
	if err != nil {
		http.Error(w, "The receipt is invalid", http.StatusBadRequest)
		return
	}

	logger.Printf("got request in process receipt\nid: %s, points: %d\n", id, points)
	io.WriteString(w, string(response))
}

// counts the points awarded by the receipt
func countPoints(r *Receipt) (int, error) {
	points := 0

	dollarsAndCents := strings.Split(r.Total, ".")
	// guaranteed to have 999.99 format as per api spec
	// so can split into dollar and cent safely
	totalCent, err := strconv.ParseInt(dollarsAndCents[1], 10, 0)
	if err != nil {
		return 0, err
	}

	// rules for point scoring
	// A: 1 per char in retailer
	// B: 50 if total is round to the dollar
	// C: 25 if total is ~.00 ~.25, ~.50, ~.75
	// D: 5 per 2 items
	// E: trim description, if len%3 == 0, add ceiling of price*0.2 points
	// F: 6 if day in purchase date is odd
	// G: 10 if purchase time is AFTER 14:00 and BEFORE 16:00 (unclear if 14:00 & 16:00 is counted)

	// A
	points += len(r.Retailer)

	// B
	if totalCent == 0 {
		points += 50
	}

	// C
	if totalCent%25 == 0 {
		points += 25
	}

	// D
	points += 5 * (len(r.Items) / 2)

	// E
	for _, item := range r.Items {
		description := strings.TrimSpace(item.ShortDescription)
		if len(description)%3 == 0 {
			itemPrice, err := strconv.ParseFloat(item.Price, 64)
			if err != nil {
				return 0, err
			}

			points += int(math.Ceil(itemPrice * 0.2))
		}
	}

	// F
	dayOfPurchase, err := strconv.ParseInt(strings.Split(r.PurchaseDate, "-")[2], 10, 0)
	if err != nil {
		return 0, err
	}

	if (dayOfPurchase & 0b1) == 1 {
		points += 6
	}

	// G
	timeofPurchase := strings.Split(r.PurchaseTime, ":")
	purchaseHour, err := strconv.ParseInt(timeofPurchase[0], 10, 0)
	if err != nil {
		return 0, err
	}

	if purchaseHour >= 14 && purchaseHour <= 16 {
		points += 10
	}

	return points, nil
}

func serve() error {
	mux := http.NewServeMux()

	mux.HandleFunc("/receipts/process", postProcessReceipt)
	mux.HandleFunc("/receipts/{id}/points", getPoints)

	fmt.Println("Serving on localhost:8080")
	return http.ListenAndServe(":8080", mux)
}
