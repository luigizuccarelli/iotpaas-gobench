package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/valyala/fasthttp"
)

var (
	requests         int64
	period           int64
	clients          int
	url              string
	urlsFilePath     string
	keepAlive        bool
	postDataFilePath string
	writeTimeout     int
	readTimeout      int
	authHeader       string
)

type Configuration struct {
	urls       []string
	method     string
	postData   []byte
	requests   int64
	period     int64
	keepAlive  bool
	authHeader string

	myClient fasthttp.Client
}

// IOTPaaS SDK schema
type IOTPaaS struct {
	Id   string       `json:"Id"`
	Name string       `json:"name"`
	Data [7]VitalSign `json:"data"` // store dtata for 7 days
}

// VitalSign struct
// this schema can be anything really
// change as needed - remember to update the fields in the consumer.go accordingly
// and the IOTPaaS struct
type VitalSign struct {
	DeviceId  string                 `json:"deviceId,omitempty"`
	PatientId string                 `json:"patientId,omitempty"`
	HR        int                    `json:"hr"`     // heart rate
	BPS       int                    `json:"bps"`    // blood pressure systolic
	BPD       int                    `json:"bpd"`    // blood pressure diastolic
	SPO2      int                    `json:"spo2"`   // blood oxygen saturation
	Custom    map[string]interface{} `json:"custom"` // custom fields - i.e respiratory rate and temperature
	Date      int64                  `json:"date,omitempty"`
}

type Result struct {
	requests      int64
	success       int64
	networkFailed int64
	badFailed     int64
}

var readThroughput int64
var writeThroughput int64

type MyConn struct {
	net.Conn
}

func (this *MyConn) Read(b []byte) (n int, err error) {
	len, err := this.Conn.Read(b)

	if err == nil {
		atomic.AddInt64(&readThroughput, int64(len))
	}

	return len, err
}

func (this *MyConn) Write(b []byte) (n int, err error) {
	len, err := this.Conn.Write(b)

	if err == nil {
		atomic.AddInt64(&writeThroughput, int64(len))
	}

	return len, err
}

func init() {
	flag.Int64Var(&requests, "r", -1, "Number of requests per client")
	flag.IntVar(&clients, "c", 100, "Number of concurrent clients")
	flag.StringVar(&url, "u", "", "URL")
	flag.StringVar(&urlsFilePath, "f", "", "URL's file path (line seperated)")
	flag.BoolVar(&keepAlive, "k", true, "Do HTTP keep-alive")
	flag.StringVar(&postDataFilePath, "d", "", "HTTP POST data file path")
	flag.Int64Var(&period, "t", -1, "Period of time (in seconds)")
	flag.IntVar(&writeTimeout, "tw", 5000, "Write timeout (in milliseconds)")
	flag.IntVar(&readTimeout, "tr", 5000, "Read timeout (in milliseconds)")
	flag.StringVar(&authHeader, "auth", "", "Authorization header")
}

func printResults(results map[int]*Result, startTime time.Time) {
	var requests int64
	var success int64
	var networkFailed int64
	var badFailed int64

	for _, result := range results {
		requests += result.requests
		success += result.success
		networkFailed += result.networkFailed
		badFailed += result.badFailed
	}

	elapsed := int64(time.Since(startTime).Seconds())

	if elapsed == 0 {
		elapsed = 1
	}

	fmt.Println()
	fmt.Printf("Requests:                       %10d hits\n", requests)
	fmt.Printf("Successful requests:            %10d hits\n", success)
	fmt.Printf("Network failed:                 %10d hits\n", networkFailed)
	fmt.Printf("Bad requests failed (!2xx):     %10d hits\n", badFailed)
	fmt.Printf("Successful requests rate:       %10d hits/sec\n", success/elapsed)
	fmt.Printf("Read throughput:                %10d bytes/sec\n", readThroughput/elapsed)
	fmt.Printf("Write throughput:               %10d bytes/sec\n", writeThroughput/elapsed)
	fmt.Printf("Test time:                      %10d sec\n", elapsed)
}

func readLines(path string) (lines []string, err error) {
	var file *os.File
	var part []byte
	var prefix bool

	if file, err = os.Open(path); err != nil {
		return
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	buffer := bytes.NewBuffer(make([]byte, 0))
	for {
		if part, prefix, err = reader.ReadLine(); err != nil {
			break
		}
		buffer.Write(part)
		if !prefix {
			lines = append(lines, buffer.String())
			buffer.Reset()
		}
	}
	if err == io.EOF {
		err = nil
	}
	return
}

func NewConfiguration() *Configuration {

	if urlsFilePath == "" && url == "" {
		flag.Usage()
		os.Exit(1)
	}

	if requests == -1 && period == -1 {
		fmt.Println("Requests or period must be provided")
		flag.Usage()
		os.Exit(1)
	}

	if requests != -1 && period != -1 {
		fmt.Println("Only one should be provided: [requests|period]")
		flag.Usage()
		os.Exit(1)
	}

	configuration := &Configuration{
		urls:       make([]string, 0),
		method:     "GET",
		postData:   nil,
		keepAlive:  keepAlive,
		requests:   int64((1 << 63) - 1),
		authHeader: authHeader}

	if period != -1 {
		configuration.period = period

		timeout := make(chan bool, 1)
		go func() {
			<-time.After(time.Duration(period) * time.Second)
			timeout <- true
		}()

		go func() {
			<-timeout
			pid := os.Getpid()
			proc, _ := os.FindProcess(pid)
			err := proc.Signal(os.Interrupt)
			if err != nil {
				log.Println(err)
				return
			}
		}()
	}

	if requests != -1 {
		configuration.requests = requests
	}

	if urlsFilePath != "" {
		fileLines, err := readLines(urlsFilePath)

		if err != nil {
			log.Fatalf("Error in ioutil.ReadFile for file: %s Error: ", urlsFilePath, err)
		}

		configuration.urls = fileLines
	}

	if url != "" {
		configuration.urls = append(configuration.urls, url)
	}

	if postDataFilePath != "" {
		configuration.method = "POST"
	}

	configuration.myClient.ReadTimeout = time.Duration(readTimeout) * time.Millisecond
	configuration.myClient.WriteTimeout = time.Duration(writeTimeout) * time.Millisecond
	configuration.myClient.MaxConnsPerHost = clients

	configuration.myClient.Dial = MyDialer()

	return configuration
}

func MyDialer() func(address string) (conn net.Conn, err error) {
	return func(address string) (net.Conn, error) {
		conn, err := net.Dial("tcp", address)
		if err != nil {
			return nil, err
		}

		myConn := &MyConn{Conn: conn}

		return myConn, nil
	}
}

func client(configuration *Configuration, result *Result, done *sync.WaitGroup) {
	for result.requests < configuration.requests {
		for _, tmpUrl := range configuration.urls {

			req := fasthttp.AcquireRequest()

			req.SetRequestURI(tmpUrl)
			req.Header.SetMethodBytes([]byte(configuration.method))

			if configuration.keepAlive == true {
				req.Header.Set("Connection", "keep-alive")
			} else {
				req.Header.Set("Connection", "close")
			}

			if len(configuration.authHeader) > 0 {
				req.Header.Set("Authorization", configuration.authHeader)
			}

			req.SetBody(getRandomData())

			resp := fasthttp.AcquireResponse()
			err := configuration.myClient.Do(req, resp)
			statusCode := resp.StatusCode()
			result.requests++
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)

			if err != nil {
				result.networkFailed++
				continue
			}

			if statusCode == fasthttp.StatusOK {
				result.success++
			} else {
				result.badFailed++
			}
		}
	}

	done.Done()
}

func getRandomData() []byte {
	var custom = make(map[string]interface{})
	s1 := rand.NewSource(time.Now().UnixNano())
	r1 := rand.New(s1)

	// number for HR
	hr := r1.Intn(191) + 30
	bps := r1.Intn(201) + 70
	bpd := r1.Intn(71) + 30
	spo2 := r1.Intn(16) + 85
	temp := r1.Intn(39) + 1
	rr := r1.Intn(28) + 1

	idInt := r1.Intn(1000) + 1
	id := "ID" + strconv.Itoa(idInt)

	t := time.Now()
	//yrday := t.Weekday()
	day := r1.Intn(7)
	custom["t"] = temp
	custom["rr"] = rr

	vs := VitalSign{DeviceId: id, HR: hr, BPS: bps, BPD: bpd, SPO2: spo2, Custom: custom, Date: t.Unix()}
	var vsArray [7]VitalSign
	vsArray[day] = vs
	data := IOTPaaS{
		Name: "iot-paas",
		Id:   id,
		Data: vsArray,
	}

	b, _ := json.MarshalIndent(&data, "", "  ")
	return b
}

func main() {

	startTime := time.Now()
	var done sync.WaitGroup
	results := make(map[int]*Result)

	signalChannel := make(chan os.Signal, 2)
	signal.Notify(signalChannel, os.Interrupt)
	go func() {
		_ = <-signalChannel
		printResults(results, startTime)
		os.Exit(0)
	}()

	flag.Parse()

	configuration := NewConfiguration()

	goMaxProcs := os.Getenv("GOMAXPROCS")

	if goMaxProcs == "" {
		runtime.GOMAXPROCS(runtime.NumCPU())
	}

	fmt.Printf("Dispatching %d clients\n", clients)

	done.Add(clients)
	for i := 0; i < clients; i++ {
		result := &Result{}
		results[i] = result
		go client(configuration, result, &done)

	}
	fmt.Println("Waiting for results...")
	done.Wait()
	printResults(results, startTime)
}
