package main

import (
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"fmt"
	"github.com/boltdb/bolt"
	"github.com/pkg/errors"
	"html/template"
	"log"
	"math/rand"
	"net/http"
	"strings"
	"time"
)

//

const (
	rBytes           = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	EXPIRATIION_TIME = 3600000
)

var (
	db        *bolt.DB // global bolt db handler
	allQItems []QuizItem
)

var (
	BUCKET_QUIZ_ITEMS        = []byte("quiz_items")
	BUCKET_PARTAKER_SESSIONS = []byte("partaker_sessions")
)

// =============================================================================
// Main data structs
// =============================================================================
type QuizAnswer struct {
	Key  string
	Text string
}

type QuizItem struct {
	Index          int
	Text           string
	Answers        []QuizAnswer
	RightAnswerKey string
}

type PartakerAnswer struct {
	QuizItemIndex int
	AnswerKey     string
}

type PartakerSession struct {
	PartakerId      string
	PartakerName    string
	PartakerPhone   string
	PartakerEmail   string
	StartTimestamp  time.Time
	FinishTimestamp time.Time
	Answers         []PartakerAnswer
	Score           int
}

// sample data
var QuizItems = []QuizItem{
	QuizItem{
		Index: 0,
		Text:  "Some question 1",
		Answers: []QuizAnswer{
			QuizAnswer{"A", "Answer 1 A"},
			QuizAnswer{"B", "Answer 1 B"},
			QuizAnswer{"C", "Answer 1 C"},
			QuizAnswer{"D", "Answer 1 D"},
		},
		RightAnswerKey: "A",
	},
	QuizItem{
		Index: 1,
		Text:  "Some question 2",
		Answers: []QuizAnswer{
			QuizAnswer{"A", "Answer 2 A"},
			QuizAnswer{"B", "Answer 2 B"},
			QuizAnswer{"C", "Answer 2 C"},
			QuizAnswer{"D", "Answer 2 D"},
		},
		RightAnswerKey: "B",
	},
}

// =============================================================================
// Helpers
// =============================================================================
func newRStr(length int) string {
	src := rand.NewSource(time.Now().UnixNano())
	r := rand.New(src)
	b := make([]byte, length)
	for i := range b {
		b[i] = rBytes[r.Intn(len(rBytes))]
	}
	return string(b)
}

func newPartakerId() string {
	return newRStr(6)
}

func GetInitQuizItems() []QuizItem {
	return QuizItems
}

// =============================================================================
// DB
// =============================================================================
// itob returns an 8-byte big endian representation of v.
func itob(v int) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(v))
	return b
}

func encodeToBytes(x interface{}) ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	err := enc.Encode(x)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decodeFromBytes(bs []byte, x interface{}) error {
	buf := bytes.NewBuffer(bs)
	return gob.NewDecoder(buf).Decode(x)
}

func initGOBTypes() {
	gob.Register(QuizItem{})
	gob.Register(PartakerSession{})
}

func resetDB() error {
	fmt.Println("resetting db")
	initQis := GetInitQuizItems()

	// create buckets and insert init data
	return db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(BUCKET_QUIZ_ITEMS)
		if err != nil {
			return fmt.Errorf("create bucket: %s", err)
		}
		for _, qi := range initQis {
			bs, err := encodeToBytes(qi)
			if err != nil {
				return fmt.Errorf("failed to encode to bytes: %s", err)
			}
			err = b.Put(itob(qi.Index), bs)
			if err != nil {
				return fmt.Errorf("failed to put into bytes: %s", err)
			}
		}

		_, err = tx.CreateBucketIfNotExists(BUCKET_PARTAKER_SESSIONS)
		if err != nil {
			return fmt.Errorf("create bucket: %s", err)
		}

		return nil
	})
}

func initDB() (*bolt.DB, error) {
	return bolt.Open("quiz.db", 0600, &bolt.Options{Timeout: 1 * time.Second})
}

func getDBQuizItems() ([]QuizItem, error) {
	var qis []QuizItem
	err := db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(BUCKET_QUIZ_ITEMS)
		b.ForEach(func(k, v []byte) error {
			var qi QuizItem
			err := decodeFromBytes(v, &qi)
			if err != nil {
				return err
			}
			qis = append(qis, qi)
			return nil
		})
		return nil
	})

	return qis, err
}

// createNewPartakerSession creates new partaker session in boltdb
// and returns created partaker's id
func createNewPartakerSession(pName, pPhone, pEmail string) (string, error) {
	pId := newPartakerId()
	ps := PartakerSession{
		PartakerId:     pId,
		PartakerName:   pName,
		PartakerPhone:  pPhone,
		PartakerEmail:  pEmail,
		StartTimestamp: time.Now(),
	}
	err := db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(BUCKET_PARTAKER_SESSIONS)
		bs, err := encodeToBytes(ps)
		if err != nil {
			return err
		}
		err = b.Put([]byte(pId), bs)
		if err != nil {
			return err
		}
		return nil
	})
	return pId, err
}

// savePartakerSessionResult updates partaker session with answers
// and returns updated partaker's name
func savePartakerSessionResult(pId string, pas []PartakerAnswer) (string, error) {
	var pName string
	err := db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(BUCKET_PARTAKER_SESSIONS)
		v := b.Get([]byte(pId))
		if v == nil {
			return errors.New("partaker session wasn't found")
		}
		var ps PartakerSession
		err := decodeFromBytes(v, &ps)
		if err != nil {
			return err
		}
		pName = ps.PartakerName

		// update
		ps.FinishTimestamp = time.Now()
		ps.Answers = pas

		bs, err := encodeToBytes(ps)
		if err != nil {
			return err
		}
		err = b.Put([]byte(pId), bs)
		if err != nil {
			return err
		}
		return nil
	})
	return pName, err
}

// =============================================================================
// Web handlers
// =============================================================================
var (
	tIndex, tQuiz, tError, tFinish *template.Template
)

func initTemplates() {
	tIndex = template.Must(template.New("index.html").ParseFiles("index.html"))
	tQuiz = template.Must(template.New("quiz.html").ParseFiles("quiz.html"))
	tError = template.Must(template.New("error.html").ParseFiles("error.html"))
	tFinish = template.Must(template.New("finish.html").ParseFiles("finish.html"))
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	tIndex.Execute(w, nil)
}

func renderErrorTemplate(w http.ResponseWriter, err error) {
	fmt.Println("ERROR")
	fmt.Println(err)
	// w.WriteHeader(http.StatusInternalServerError)
	tError.Execute(w, err.Error())
}

type QuizVM struct {
	PartakerId     string
	ExpirationTime int
	QuizItems      []QuizItem
}

func startHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Println("handle start")

	pName := strings.TrimSpace(r.FormValue("partaker_name"))
	pPhone := strings.TrimSpace(r.FormValue("partaker_phone"))
	pEmail := strings.TrimSpace(r.FormValue("partaker_email"))

	fmt.Println("pName: " + pName)
	fmt.Println("pPhone: " + pPhone)
	if pName == "" || pPhone == "" {
		// @@TODO: return error page
		renderErrorTemplate(w, errors.New("Phone and name not passed"))
		return
	}

	// @@TODO: check if already exists same session ?

	// create new session
	pId, err := createNewPartakerSession(pName, pPhone, pEmail)
	if err != nil {
		renderErrorTemplate(w, errors.Wrap(err, "Failed to create new partaker session"))
		return
	}

	qvm := QuizVM{
		PartakerId:     pId,
		ExpirationTime: EXPIRATIION_TIME,
		QuizItems:      allQItems,
	}

	// return quiz html fragment with partaker id
	err = tQuiz.Execute(w, qvm)
	if err != nil {
		renderErrorTemplate(w, errors.Wrap(err, "failed to execute tQuiz"))
		return
	}
}

type FinishVM struct {
	PartakerName string
}

func saveResultsHandler(w http.ResponseWriter, r *http.Request) {
	// @@TODO: check if time hasn't expired
	fmt.Println("save results")
	err := r.ParseForm()
	if err != nil {
		renderErrorTemplate(w, errors.New("failed to parse"))
		return
	}

	pId := r.PostFormValue("partaker_id")
	if pId == "" {
		renderErrorTemplate(w, errors.New("Partaker id wasn't found"))
		return
	}

	// get answers from form
	// (assumes that there must be as many partaker answers, as quiz items)
	pas := make([]PartakerAnswer, len(allQItems))
	for i, qi := range allQItems {
		ak := fmt.Sprintf("answer_key_for_%v", qi.Index)
		val := r.PostFormValue(ak)
		if val == "" {
			renderErrorTemplate(w, errors.New("answer value wasn't found for key "+ak))
			return
		}
		pas[i] = PartakerAnswer{QuizItemIndex: qi.Index, AnswerKey: val}
	}
	pName, err := savePartakerSessionResult(pId, pas)
	if err != nil {
		renderErrorTemplate(w, errors.New("Failed to save partaker session"))
		return
	}

	// if ok, then show Finish page
	err = tFinish.Execute(w, FinishVM{PartakerName: pName})
	if err != nil {
		renderErrorTemplate(w, errors.Wrap(err, "failed to execute tFinish"))
		return
	}
}

type myfs struct {
	http.Dir
}

func initRoutes() {
	http.HandleFunc("/", indexHandler)
	http.HandleFunc("/start", startHandler)
	http.HandleFunc("/saveResults", saveResultsHandler)
	fs := http.FileServer(myfs{http.Dir("./public/")})
	http.HandleFunc("/static/", http.StripPrefix("/static/", fs).ServeHTTP)
}

// =============================================================================
// Main func
// =============================================================================
func main() {
	fmt.Println("Start brk quiz")

	newdb, err := initDB()
	if err != nil {
		log.Fatal(err)
	}
	db = newdb
	defer db.Close()

	err = resetDB()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("DONE: reset db")

	initGOBTypes()

	// get all quiz items from db once and cache into global var
	qis, err := getDBQuizItems()
	if err != nil {
		log.Fatal(err)
	}
	allQItems = qis

	// init
	initTemplates()
	initRoutes()

	// run server
	log.Fatal(http.ListenAndServe(":8080", nil))
}
