package api

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/sorucoder/hackpsu-2022-kcf-industry-challenge-4.0/api/hardware"
)

type TabulatedHardwareRequestData struct {
	Id    string    `json:"id"`
	From  time.Time `json:"from"`
	To    time.Time `json:"to"`
	Count int       `json:"count"`
}

func Handle(response http.ResponseWriter, request *http.Request) {
	switch request.URL.Path {
	case "/api/tabulated_hardware":
		if request.Method == "POST" {
			dataBytes, err := io.ReadAll(request.Body)
			if err != nil {
				response.WriteHeader(http.StatusInternalServerError)
				return
			}

			var requestData TabulatedHardwareRequestData
			if err := json.Unmarshal(dataBytes, &requestData); err != nil {
				response.WriteHeader(http.StatusInternalServerError)
				return
			}

			if !hardware.HasSamples(requestData.Id) {
				response.WriteHeader(http.StatusInternalServerError)
				return
			}

			tabulatedHardware := make(map[string]*hardware.Sample)
			for timestamp := requestData.From; timestamp.Before(requestData.To); timestamp.Add(time.Duration(requestData.To.Sub(requestData.From).Abs().Nanoseconds() / int64(requestData.Count))) {
				sample, err := hardware.InterpolateSample(requestData.Id, timestamp)
				if err != nil {
					response.WriteHeader(http.StatusInternalServerError)
					return
				}
				tabulatedHardware[timestamp.Format("January _2, 2006 _3:04:05.999PM")] = sample
			}

			tabulatedHardwareBytes, err := json.Marshal(tabulatedHardware)
			if err != nil {
				response.WriteHeader(http.StatusInternalServerError)
				return
			}

			response.WriteHeader(http.StatusOK)
			response.Write(tabulatedHardwareBytes)
			return
		}
	default:
		response.Write([]byte("Welcome!"))
	}

}
