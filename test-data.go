package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"strconv"
	"time"
)

// IOTPaaS SDK schema
type IOTPaaS struct {
	PatientId string `json:"patientId"`
	DeviceId  string `json:"deviceId"`
	Name      string `json:"name"`
	Data      [7]VitalSign
}

// VitalSign struct
// this schema can be anything really
// change as needed - remember to update the fields in the consumer.go accordingly
type VitalSign struct {
	HR     int                    `json:"hr"`   // heart rate
	BPS    int                    `json:"bps"`  // blood pressure systolic
	BPD    int                    `json:"bpd"`  // blood pressure diastolic
	SPO2   int                    `json:"spo2"` // blood oxygen saturation
	Custom map[string]interface{} `json:"custom"`
	Date   string                 `json:"date,omitempty"`
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

	vs := VitalSign{HR: hr, BPS: bps, BPD: bpd, SPO2: spo2, Custom: custom, Date: t.Format("2006-01-02")}
	var vsArray [7]VitalSign
	vsArray[day] = vs
	data := IOTPaaS{
		Name:      "iot-paas",
		DeviceId:  id,
		PatientId: id,
		Data:      vsArray,
	}

	b, _ := json.MarshalIndent(&data, "", "  ")
	return b

}

func main() {

	fmt.Println("Start")
	b := getRandomData()
	fmt.Println("Result", string(b))

}
