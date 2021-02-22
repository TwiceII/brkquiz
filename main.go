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
	"strconv"
	"strings"
	"time"
)

//

const (
	rBytes           = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	EXPIRATIION_TIME = 3600000
	// EXPIRATIION_TIME = 3000
)

var (
	ESessionNotFound = errors.New("partaker session wasn't found")
)

var (
	db        *bolt.DB // global bolt db handler
	allQItems []QuizItem
	allWaves  []SessionWave
)

var (
	BUCKET_QUIZ_ITEMS        = []byte("quiz_items")
	BUCKET_PARTAKER_SESSIONS = []byte("partaker_sessions")
	BUCKET_SESSION_WAVES     = []byte("session_waves")
	BUCKET_SUBSCRIPTIONS     = []byte("subscriptions")
)

// =============================================================================
// Main data structs
// =============================================================================
// поток (может быть несколько в день)
type SessionWave struct {
	WaveId             string
	RegStartTimestamp  time.Time
	RegFinishTimestamp time.Time
}

type QuizAnswer struct {
	Index int
	Text  string
}

type QuizItem struct {
	Index            int
	Text             string
	WaveId           string
	Answers          []QuizAnswer
	RightAnswerIndex int
}

type PartakerAnswer struct {
	QuizItemIndex int
	AnswerIndex   int
}

type PartakerSession struct {
	WaveId          string
	PartakerId      string
	PartakerName    string
	PartakerPhone   string
	PartakerEmail   string
	StartTimestamp  time.Time
	FinishTimestamp time.Time
	Answers         []PartakerAnswer
	Score           int
}

type Subscription struct {
	Email         string
	Phone         string
	SubsTimestamp time.Time
}

// sample data
var QuizItems = []QuizItem{
	QuizItem{
		Index:  0,
		WaveId: "first",
		Text:   "Some question 1",
		Answers: []QuizAnswer{
			QuizAnswer{1, "Answer 1 1"},
			QuizAnswer{2, "Answer 1 2"},
			QuizAnswer{3, "Answer 1 3"},
			QuizAnswer{4, "Answer 1 4"},
		},
		RightAnswerIndex: 1,
	},
	QuizItem{
		Index:  1,
		WaveId: "second",
		Text:   "Some question 2",
		Answers: []QuizAnswer{
			QuizAnswer{1, "Answer 2 1"},
			QuizAnswer{2, "Answer 2 2"},
			QuizAnswer{3, "Answer 2 3"},
			QuizAnswer{4, "Answer 2 4"},
		},
		RightAnswerIndex: 2,
	},
	QuizItem{
		Index:  3,
		WaveId: "second",
		Text:   "Some question 3",
		Answers: []QuizAnswer{
			QuizAnswer{1, "Answer 3 1"},
			QuizAnswer{2, "Answer 3 2"},
			QuizAnswer{3, "Answer 3 3"},
			QuizAnswer{4, "Answer 3 4"},
		},
		RightAnswerIndex: 4,
	},
	QuizItem{
		Index:  4,
		WaveId: "second",
		Text:   "Some question 4",
		Answers: []QuizAnswer{
			QuizAnswer{1, "Answer 4 1"},
			QuizAnswer{2, "Answer 4 2"},
			QuizAnswer{3, "Answer 4 3"},
			QuizAnswer{4, "Answer 4 4"},
		},
		RightAnswerIndex: 3,
	},
}

var almatyTZ, _ = time.LoadLocation("Asia/Almaty")
var Waves = []SessionWave{
	SessionWave{
		WaveId:             "first",
		RegStartTimestamp:  time.Date(2021, 2, 13, 10, 0, 0, 0, almatyTZ),
		RegFinishTimestamp: time.Date(2021, 2, 13, 10, 15, 0, 0, almatyTZ),
	},
	SessionWave{
		WaveId:             "second",
		RegStartTimestamp:  time.Date(2021, 2, 13, 15, 0, 0, 0, almatyTZ),
		RegFinishTimestamp: time.Date(2021, 2, 13, 19, 15, 0, 0, almatyTZ),
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

func newSubscriptionId() string {
	return newRStr(6)
}

func GetInitQuizItems() []QuizItem {
	return QuizItems
}

func ShuffleQuizItemsWithAnswers(qis []QuizItem) {
	// shuffle quiz items
	rand.Seed(time.Now().UnixNano())
	rand.Shuffle(len(qis), func(i, j int) { qis[i], qis[j] = qis[j], qis[i] })
	// shuffle answers in each quiz item
	for idx, qi := range qis {
		rand.Shuffle(len(qi.Answers), func(i, j int) {
			qis[idx].Answers[i], qis[idx].Answers[j] = qis[idx].Answers[j], qis[idx].Answers[i]
		})
	}
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

		_, err = tx.CreateBucketIfNotExists(BUCKET_SUBSCRIPTIONS)
		if err != nil {
			return fmt.Errorf("create bucket: %s", err)
		}

		_, err = tx.CreateBucketIfNotExists(BUCKET_PARTAKER_SESSIONS)
		if err != nil {
			return fmt.Errorf("create bucket: %s", err)
		}

		bw, err := tx.CreateBucketIfNotExists(BUCKET_SESSION_WAVES)
		if err != nil {
			return fmt.Errorf("create bucket: %s", err)
		}

		for _, sw := range Waves {
			bs, err := encodeToBytes(sw)
			if err != nil {
				return fmt.Errorf("failed to encode to bytes: %s", err)
			}
			err = bw.Put([]byte(sw.WaveId), bs)
			if err != nil {
				return fmt.Errorf("failed to put into bytes: %s", err)
			}
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

func getDBSessionWaves() ([]SessionWave, error) {
	var waves []SessionWave
	err := db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(BUCKET_SESSION_WAVES)
		b.ForEach(func(k, v []byte) error {
			var w SessionWave
			err := decodeFromBytes(v, &w)
			if err != nil {
				return err
			}
			waves = append(waves, w)
			return nil
		})
		return nil
	})

	return waves, err
}

func GetPartakerSessionById(pId string) (PartakerSession, error) {
	var ps PartakerSession
	err := db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(BUCKET_PARTAKER_SESSIONS)
		v := b.Get([]byte(pId))
		if v == nil {
			return ESessionNotFound
		}
		err := decodeFromBytes(v, &ps)
		if err != nil {
			return err
		}
		return nil
	})
	return ps, err
}

func createSubscription(email, phone string) error {
	id := newSubscriptionId()
	err := db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(BUCKET_SUBSCRIPTIONS)
		bs, err := encodeToBytes(&Subscription{
			Id:            id,
			Email:         email,
			Phone:         phone,
			SubsTimestamp: time.Now(),
		})
		if err != nil {
			return err
		}
		err = b.Put([]byte(id), bs)
		if err != nil {
			return err
		}
		return nil
	})
	return err
}

// createNewPartakerSession creates new partaker session in boltdb
// and returns created partaker's id
func createNewPartakerSession(pName, pPhone, pEmail string, wv SessionWave) (string, error) {
	pId := newPartakerId()
	ps := PartakerSession{
		WaveId:         wv.WaveId,
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
	tIndex, tPromo, tSubSuccess, tWavesList, tQuiz, tError, tFinish *template.Template
)

func initTemplates() {
	tIndex = template.Must(template.New("index.html").ParseFiles("index.html"))
	tPromo = template.Must(template.New("promo.html").ParseFiles("promo.html"))
	tSubSuccess = template.Must(template.New("sub-success.html").ParseFiles("sub-success.html"))
	tQuiz = template.Must(template.New("quiz.html").ParseFiles("quiz.html"))
	tError = template.Must(template.New("error.html").ParseFiles("error.html"))
	tFinish = template.Must(template.New("finish.html").ParseFiles("finish.html"))
	tWavesList = template.Must(template.New("waves.html").ParseFiles("waves.html"))
}

func RenderWavesList(w http.ResponseWriter) {
	tWavesList.Execute(w, allWaves)
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	curWave := GetCurrentSessionWave()
	if curWave != nil {
		tIndex.Execute(w, nil)
	} else {
		RenderWavesList(w)
	}
}

func promoHandler(w http.ResponseWriter, r *http.Request) {
	tPromo.Execute(w, nil)
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

func GetCurrentSessionWave() *SessionWave {
	now := time.Now()
	var foundWave *SessionWave
	for _, wv := range allWaves {
		if now.After(wv.RegStartTimestamp) && now.Before(wv.RegFinishTimestamp) {
			foundWave = &wv
			break
		}
	}
	return foundWave
}

func startHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Println("handle start")

	// check session wave
	curWave := GetCurrentSessionWave()
	if curWave == nil {
		RenderWavesList(w)
	}

	// if at proper session wave
	pName := strings.TrimSpace(r.FormValue("partaker_name"))
	pPhone := strings.TrimSpace(r.FormValue("partaker_phone"))
	pEmail := strings.TrimSpace(r.FormValue("partaker_email"))

	fmt.Println("pName: " + pName)
	fmt.Println("pPhone: " + pPhone)
	if pName == "" || pPhone == "" {
		renderErrorTemplate(w, errors.New("Phone and name not passed"))
		return
	}

	// @@TODO: check if already exists same session ?

	// create new session
	pId, err := createNewPartakerSession(pName, pPhone, pEmail, *curWave)
	if err != nil {
		renderErrorTemplate(w, errors.Wrap(err, "Failed to create new partaker session"))
		return
	}

	// filter quiz items by wave and shuffle them
	var qItems []QuizItem
	for _, qi := range allQItems {
		if qi.WaveId == curWave.WaveId {
			qItems = append(qItems, qi)
		}
	}
	ShuffleQuizItemsWithAnswers(qItems)
	fmt.Println("Shuffled qItems")
	fmt.Println(qItems)

	qvm := QuizVM{
		PartakerId:     pId,
		ExpirationTime: EXPIRATIION_TIME,
		QuizItems:      qItems,
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

func subscribeHandler(w http.ResponseWriter, r *http.Request) {
	err := r.ParseForm()
	if err != nil {
		renderSubErrorTemplate(w, errors.New("failed to parse"))
		return
	}

	email := r.PostFormValue("email")
	if email == "" {
		renderSubErrorTemplate(w, errors.New("Email wasn't found"))
		return
	}

	err = createSubscription(email, phone)
	if err != nil {
		renderSubErrorTemplate(w, errors.New("Failed to create subscription"))
		return
	}

	err = tSubSuccess.Execute(w, nil)
	if err != nil {
		renderSubErrorTemplate(w, errors.New("Failed to execute template"))
		return
	}
}

func saveResultsHandler(w http.ResponseWriter, r *http.Request) {
	now := time.Now()

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

	pSession, err := GetPartakerSessionById(pId)
	if err != nil {
		renderErrorTemplate(w, errors.New("Error while getting partaker session"))
		return
	}

	// check if time hasn't expired
	exp := pSession.StartTimestamp.Add(time.Millisecond*EXPIRATIION_TIME +
		// additional minute for latency delays etc.
		// time.Millisecond*1000)
		time.Millisecond*60000)
	if now.After(exp) {
		renderErrorTemplate(w, errors.New("time has expired"))
		return
	}

	// get answers from form
	// there might be less answers than quiz items
	var pas []PartakerAnswer

	for _, qi := range allQItems {
		ak := fmt.Sprintf("answer_key_for_%v", qi.Index)
		val := r.PostFormValue(ak)
		// if answer wasn't provided for this quiz item, keep on
		if val == "" {
			continue
		}
		// else parse and add
		iVal, err := strconv.Atoi(val)
		if err != nil {
			renderErrorTemplate(w, errors.New("failed to convert to int answer index "+val))
			return
		}
		pas = append(pas, PartakerAnswer{QuizItemIndex: qi.Index, AnswerIndex: iVal})
	}
	fmt.Printf("got %v answers\n", len(pas))
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
	http.HandleFunc("/promo", promoHandler)
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

	// err = resetDB()
	// if err != nil {
	// 	log.Fatal(err)
	// }
	// fmt.Println("DONE: reset db")

	initGOBTypes()

	// get all quiz items and waves from db once and cache into global var
	qis, err := getDBQuizItems()
	if err != nil {
		log.Fatal(err)
	}
	allQItems = qis
	fmt.Println(allQItems)
	wvs, err := getDBSessionWaves()
	if err != nil {
		log.Fatal(err)
	}
	allWaves = wvs
	fmt.Println(allWaves)

	ShuffleQuizItemsWithAnswers(allQItems)
	fmt.Println(allQItems)

	// init
	initTemplates()
	initRoutes()

	// run server
	log.Fatal(http.ListenAndServe(":8080", nil))
}
