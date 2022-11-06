package hardware

import (
	"encoding/csv"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"time"
	"unsafe"
)

type Sample struct {
	Time              time.Time `json:"-"`
	Temperature       *float64  `file:"temperature.csv" json:"temperature"`
	PeakVelocityX     *float64  `file:"peak_velocity_x.csv" json:"peakVelocityX"`
	RMSVelocityX      *float64  `file:"rms_velocity_x.csv" json:"rmsVelocityX"`
	PeakAccelerationX *float64  `file:"peak_acceleration_x.csv" json:"peakAccelerationX"`
	RMSAccelerationX  *float64  `file:"rms_acceleration_x.csv" json:"rmsAccelerationX"`
	PeakVelocityY     *float64  `file:"peak_velocity_y.csv" json:"peakVelocityY"`
	RMSVelocityY      *float64  `file:"rms_velocity_y.csv" json:"rmsVelocityY"`
	PeakAccelerationY *float64  `file:"peak_acceleration_y.csv" json:"peakAccelerationY"`
	RMSAccelerationY  *float64  `file:"rms_acceleration_y.csv" json:"rmsAccelerationY"`
}

func (sample *Sample) SetValueByDataFile(targetFileName string, value interface{}) bool {
	sampleType := reflect.TypeOf(sample).Elem()
	sampleValue := reflect.ValueOf(sample).Elem()

	for fieldIndex := 0; fieldIndex < sampleType.NumField(); fieldIndex++ {
		if fileName, hasFileTag := sampleType.Field(fieldIndex).Tag.Lookup("file"); hasFileTag && fileName == targetFileName {
			sampleValue.Field(fieldIndex).Set(reflect.ValueOf(value))
			return true
		}
	}
	return false
}

var (
	hardware    map[string]map[int64]*Sample
	samplesPath string       = filepath.Join("api", "hardware", "samples")
	sampleType  reflect.Type = reflect.TypeOf((*Sample)(nil)).Elem()
)

func SampleCount() int {
	var count int
	for hardwareId := range hardware {
		count += len(hardware[hardwareId])
	}
	return count
}

func PopulateSamples() {
	hardware = make(map[string]map[int64]*Sample)

	if sampleWalkErr := filepath.WalkDir(samplesPath, func(sampleFilePath string, directoryEntry fs.DirEntry, pathErr error) error {
		if !directoryEntry.IsDir() {
			samplePath, sampleDataName := filepath.Split(sampleFilePath)
			hardwareId := filepath.Base(samplePath)

			// Open data sampleDataFile and prepare for CSV reading
			sampleDataFile, openErr := os.Open(sampleFilePath)
			if openErr != nil {
				return fmt.Errorf(`unable to open file %s: %w`, sampleFilePath, openErr)
			}
			defer sampleDataFile.Close()
			sampleDataReader := csv.NewReader(sampleDataFile)

			// Read each CSV row into memory
			for {
				sampleData, readErr := sampleDataReader.Read()
				if readErr != nil {
					if readErr == io.EOF {
						break
					} else {
						return fmt.Errorf(`unable to read hardware data file "%s": %w`, sampleFilePath, readErr)
					}
				}

				var sampleTimestamp int64
				if timestamp, convertErr := strconv.ParseInt(sampleData[0], 10, 64); convertErr == nil {
					sampleTimestamp = timestamp
				} else {
					return fmt.Errorf(`cannot convert timestamp "%s" in hardware data file "%s": %w`, sampleData[0], sampleFilePath, convertErr)
				}

				var sampleDataValue float64
				if value, convertErr := strconv.ParseFloat(sampleData[1], 64); convertErr == nil {
					sampleDataValue = value
				} else {
					return fmt.Errorf(`cannot convert value "%s" in hardware data file "%s": %w`, sampleData[1], sampleFilePath, convertErr)
				}

				_, hardwareExists := hardware[hardwareId]
				if !hardwareExists {
					hardware[hardwareId] = make(map[int64]*Sample)
				}

				sample, sampleExists := hardware[hardwareId][sampleTimestamp]
				if !sampleExists {
					sample = &Sample{}
					sample.Time = time.UnixMilli(sampleTimestamp)
					hardware[hardwareId][sampleTimestamp] = sample
				}

				success := sample.SetValueByDataFile(sampleDataName, &sampleDataValue)
				if !success {
					return fmt.Errorf(`hardware schema does not support file "%s"`, sampleFilePath)
				}
			}
		}
		return nil
	}); sampleWalkErr != nil {
		panic(fmt.Errorf(`unable to populate hardware data: %w`, sampleWalkErr))
	}
}

func HasSamples(hardwareId string) bool {
	_, hasHardware := hardware[hardwareId]
	return hasHardware
}

func InterpolateSample(hardwareId string, at time.Time) (*Sample, error) {
	if !HasSamples(hardwareId) {
		return nil, fmt.Errorf(`no hardware data for "%s"`, hardwareId)
	}

	sampleCount := len(hardware[hardwareId])

	atTimestamp := at.UnixMilli()

	timestamps := make([]int64, 0, sampleCount)
	for timestamp := range hardware[hardwareId] {
		timestamps = append(timestamps, timestamp)
	}
	sort.Slice(timestamps, func(leftIndex, rightIndex int) bool { return timestamps[leftIndex] < timestamps[rightIndex] })

	var averageInterval int64
	{
		var sumOfIntervals float64
		for index := 0; index < sampleCount-1; index++ {
			sumOfIntervals += float64(timestamps[index+1]) - float64(timestamps[index])
		}
		averageInterval = int64(sumOfIntervals / float64(sampleCount))
	}
	if atTimestamp < timestamps[0]+averageInterval || atTimestamp > timestamps[sampleCount-1]-averageInterval {
		return nil, fmt.Errorf(`no interpolable hardware samples within timestamp %s`, at)
	}

	atSampleIndex := sort.Search(sampleCount, func(index int) bool { return timestamps[index] >= atTimestamp })

	interpolatedSample := reflect.New(sampleType)
	interpolatedSample.Elem().FieldByName("Time").Set(reflect.ValueOf(at))

	for fieldIndex := 0; fieldIndex < sampleType.NumField(); fieldIndex++ {
		if _, hasFileTag := sampleType.Field(fieldIndex).Tag.Lookup("file"); hasFileTag {
			var leftSample reflect.Value
			var leftTimestamp int64
			{
				leftSampleIndex := atSampleIndex
				for {
					leftTimestamp = timestamps[leftSampleIndex]
					leftSample = reflect.ValueOf(hardware[hardwareId][leftTimestamp]).Elem()
					leftSampleIndex--
					if !leftSample.Field(fieldIndex).IsNil() || leftSampleIndex <= 0 {
						break
					}
				}
			}

			var rightSample reflect.Value
			var rightTimestamp int64
			{
				rightSampleIndex := atSampleIndex
				for {
					rightTimestamp = timestamps[rightSampleIndex]
					rightSample = reflect.ValueOf(hardware[hardwareId][rightTimestamp]).Elem()
					rightSampleIndex++
					if !rightSample.Field(fieldIndex).IsNil() || rightSampleIndex >= sampleCount-1 {
						break
					}
				}
			}

			leftSampleValue := leftSample.Field(fieldIndex).Elem().Float()
			rightSampleValue := rightSample.Field(fieldIndex).Elem().Float()
			timestampInterval := float64(atTimestamp) / (float64(leftTimestamp) + float64(rightTimestamp))
			interval := 0.5 * (1.0 - math.Cos(math.Pi*timestampInterval))
			atSampleValue := leftSampleValue*(1.0-interval) + rightSampleValue*interval
			interpolatedSample.Elem().Field(fieldIndex).Set(reflect.ValueOf(&atSampleValue))
		}
	}
	return (*Sample)(unsafe.Pointer(interpolatedSample.Pointer())), nil
}
