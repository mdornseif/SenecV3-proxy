package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Cache structure to store data with timestamp
type Cache struct {
	data      map[string]interface{}
	timestamp time.Time
	mutex     sync.Mutex
}

// SenecClient handles communication with the SENEC system
type SenecClient struct {
	baseURL string
	client  *http.Client
	cache   *Cache
	// Channel for receiving data updates
	dataChan chan map[string]interface{}
	// Channel for receiving errors
	errChan chan error
	// Context for cancellation
	ctx    context.Context
	cancel context.CancelFunc
}

// NewSenecClient creates a new SENEC client
func NewSenecClient(ipAddress string) *SenecClient {
	// Create HTTP client that accepts invalid certificates
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client := &http.Client{Transport: tr}

	ctx, cancel := context.WithCancel(context.Background())

	return &SenecClient{
		baseURL:  fmt.Sprintf("https://%s/lala.cgi", ipAddress),
		client:   client,
		cache:    &Cache{data: nil, timestamp: time.Time{}, mutex: sync.Mutex{}},
		dataChan: make(chan map[string]interface{}, 1),
		errChan:  make(chan error, 1),
		ctx:      ctx,
		cancel:   cancel,
	}
}

// flattenData converts nested maps into a flat map with dot notation keys
func (s *SenecClient) flattenData(data map[string]interface{}) map[string]interface{} {
	flat := make(map[string]interface{})

	var flatten func(prefix string, value interface{})
	flatten = func(prefix string, value interface{}) {
		switch v := value.(type) {
		case map[string]interface{}:
			for key, val := range v {
				newKey := key
				if prefix != "" {
					newKey = prefix + "x" + key
				}
				flatten(newKey, val)
			}
		case []interface{}:
			// Handle arrays by creating distinct keys for each element
			for i, elem := range v {
				newKey := fmt.Sprintf("%sx%d", prefix, i)
				flatten(newKey, elem)
			}
		default:
			flat[prefix] = value

			// Add kW calculations for power values
			if strings.HasSuffix(prefix, "GUI_INVERTER_POWER") ||
				strings.HasSuffix(prefix, "GUI_HOUSE_POW") ||
				strings.HasSuffix(prefix, "GUI_GRID_POW") ||
				strings.HasSuffix(prefix, "GUI_BAT_DATA_POWER") {
				if floatVal, ok := v.(float64); ok {
					flat[prefix+"kW"] = floatVal / 1000.0
				}
			}
		}
	}

	flatten("", data)
	return flat
}

// GetData retrieves data from the SENEC system with caching
func (s *SenecClient) GetData() (map[string]interface{}, error) {
	// Check cache first
	s.cache.mutex.Lock()
	if s.cache.data != nil && time.Since(s.cache.timestamp) < 30*time.Second {
		data := s.flattenData(s.cache.data)
		s.cache.mutex.Unlock()
		return data, nil
	}
	s.cache.mutex.Unlock()

	// Create payload for SENEC API
	payload := map[string]interface{}{
		"PM1OBJ1": map[string]string{
			"FREQ":    "",
			"U_AC":    "",
			"I_AC":    "",
			"P_AC":    "",
			"P_TOTAL": "",
		},
		"ENERGY": map[string]string{
			"GUI_BAT_DATA_COLLECTED":   "",
			"GUI_BAT_DATA_CURRENT":     "",
			"GUI_BAT_DATA_FUEL_CHARGE": "",
			"GUI_BAT_DATA_POWER":       "",
			"GUI_BAT_DATA_VOLTAGE":     "",
			"GUI_GRID_POW":             "",
			"GUI_HOUSE_POW":            "",
			"GUI_INVERTER_POWER":       "",
			"STAT_HOURS_OF_OPERATION":  "",
			"STAT_STATE":               "",
		},
		"PV1": map[string]string{
			"POWER_RATIO":    "",
			"POWER_RATIO_L1": "",
			"POWER_RATIO_L2": "",
			"POWER_RATIO_L3": "",
			"MPP_VOL":        "",
			"MPP_CUR":        "",
			"MPP_POWER":      "",
			"MPP_AVAIL":      "",
		},
		"BAT1OBJ1": map[string]string{
			"TEMP1":       "",
			"TEMP2":       "",
			"S":           "",
			"P":           "",
			"Q":           "",
			"SW_VERSION":  "",
			"SW_VERSION2": "",
			"SW_VERSION3": "",
			"I_DC":        "",
		},
		"BAT1": map[string]string{
			"CEI_LIMIT":      "",
			"SPARE_CAPACITY": "",
		},
	}

	// Start async data fetch
	go s.fetchDataFromDevice(payload)

	// Try to use cached data if available while waiting for new data
	s.cache.mutex.Lock()
	if s.cache.data != nil {
		cachedData := s.flattenData(s.cache.data)
		s.cache.mutex.Unlock()
		return cachedData, nil
	}
	s.cache.mutex.Unlock()

	// Wait for new data with timeout
	select {
	case data := <-s.dataChan:
		// Update cache with new data
		s.cache.mutex.Lock()
		s.cache.data = data
		s.cache.timestamp = time.Now()
		s.cache.mutex.Unlock()
		return s.flattenData(data), nil
	case err := <-s.errChan:
		// Try to use cached data if available
		s.cache.mutex.Lock()
		if s.cache.data != nil {
			fmt.Fprintf(os.Stderr, "Error fetching data, using cached data: %v\n", err)
			data := s.flattenData(s.cache.data)
			s.cache.mutex.Unlock()
			return data, nil
		}
		s.cache.mutex.Unlock()
		return nil, err
	case <-time.After(5 * time.Second):
		// Timeout after 5 seconds
		s.cache.mutex.Lock()
		if s.cache.data != nil {
			fmt.Fprintf(os.Stderr, "Timeout fetching data, using cached data\n")
			data := s.flattenData(s.cache.data)
			s.cache.mutex.Unlock()
			return data, nil
		}
		s.cache.mutex.Unlock()
		return nil, fmt.Errorf("timeout waiting for data")
	}
}

// fetchDataFromDevice sends a request to the SENEC system and decodes the response
func (s *SenecClient) fetchDataFromDevice(payload interface{}) {
	// Convert payload to JSON
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		s.errChan <- fmt.Errorf("error marshaling payload: %v", err)
		return
	}

	// Create request with context
	req, err := http.NewRequestWithContext(s.ctx, "POST", s.baseURL, bytes.NewBuffer(jsonPayload))
	if err != nil {
		s.errChan <- fmt.Errorf("error creating request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	// Send request
	resp, err := s.client.Do(req)
	if err != nil {
		s.errChan <- fmt.Errorf("error sending request: %v", err)
		return
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode != http.StatusOK {
		s.errChan <- fmt.Errorf("received non-OK response: %s", resp.Status)
		return
	}

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		s.errChan <- fmt.Errorf("error reading response: %v", err)
		return
	}

	// Parse response JSON
	var responseData map[string]interface{}
	if err := json.Unmarshal(body, &responseData); err != nil {
		s.errChan <- fmt.Errorf("error parsing JSON: %v", err)
		return
	}

	// Decode the SENEC-specific encoding
	decodedData, err := s.decodeResponse(responseData)
	if err != nil {
		s.errChan <- fmt.Errorf("error decoding response: %v", err)
		return
	}

	s.dataChan <- decodedData
}

// decodeResponse decodes the SENEC-specific encoding
func (s *SenecClient) decodeResponse(data map[string]interface{}) (map[string]interface{}, error) {
	decodedData := make(map[string]interface{})

	for category, values := range data {
		if valuesMap, ok := values.(map[string]interface{}); ok {
			decodedCategory := make(map[string]interface{})
			for key, value := range valuesMap {
				decodedValue, err := s.decodeAnyValue(value)
				if err != nil {
					return nil, fmt.Errorf("error decoding value for %s.%s: %v", category, key, err)
				}
				decodedCategory[key] = decodedValue
			}
			decodedData[category] = decodedCategory
		}
	}

	return decodedData, nil
}

// decodeAnyValue decodes any value from the SENEC response
func (s *SenecClient) decodeAnyValue(value interface{}) (interface{}, error) {
	switch v := value.(type) {
	case []interface{}:
		decodedArray := make([]interface{}, len(v))
		for i, item := range v {
			decodedItem, err := s.decodeAnyValue(item)
			if err != nil {
				return nil, err
			}
			decodedArray[i] = decodedItem
		}
		return decodedArray, nil
	case map[string]interface{}:
		decodedObj := make(map[string]interface{})
		for k, val := range v {
			decodedVal, err := s.decodeAnyValue(val)
			if err != nil {
				return nil, err
			}
			decodedObj[k] = decodedVal
		}
		return decodedObj, nil
	case string:
		return s.decodeValue(v)
	default:
		return value, nil
	}
}

// decodeValue decodes a single value from the SENEC response
func (s *SenecClient) decodeValue(value string) (interface{}, error) {
	if strings.HasPrefix(value, "fl_") {
		// Floating point value
		return s.decodeFloat(value[3:])
	} else if strings.HasPrefix(value, "u8_") {
		// Unsigned 8-bit integer
		return s.decodeUint8(value[3:])
	} else if strings.HasPrefix(value, "u1_") {
		// Unsigned 16-bit integer
		return s.decodeUint16(value[3:])
	} else if strings.HasPrefix(value, "u3_") {
		// Unsigned 32-bit integer
		return s.decodeUint32(value[3:])
	} else if strings.HasPrefix(value, "i1_") {
		// Signed 16-bit integer
		return s.decodeInt16(value[3:])
	} else if strings.HasPrefix(value, "i3_") {
		// Signed 32-bit integer
		return s.decodeInt32(value[3:])
	} else if strings.HasPrefix(value, "i8_") {
		// Signed 8-bit integer
		return s.decodeInt8(value[3:])
	} else if strings.HasPrefix(value, "st_") {
		// String value
		return s.decodeString(value[3:])
	} else {
		// Return as is
		return value, nil
	}
}

// decodeFloat decodes a floating point value
func (s *SenecClient) decodeFloat(hexStr string) (float64, error) {
	if hexStr == "" {
		return 0.0, nil
	}

	// Convert hex string to uint32
	bits, err := strconv.ParseUint(hexStr, 16, 32)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid float hex: %s\n", hexStr)
		return 0.0, nil
	}

	// Convert bits to float32 and then to float64
	return float64(math.Float32frombits(uint32(bits))), nil
}

// decodeUint8 decodes an unsigned 8-bit integer
func (s *SenecClient) decodeUint8(hexStr string) (uint8, error) {
	if hexStr == "" {
		return 0, nil
	}

	val, err := strconv.ParseUint(hexStr, 16, 8)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid u8 hex: %s\n", hexStr)
		return 0, nil
	}

	return uint8(val), nil
}

// decodeUint16 decodes an unsigned 16-bit integer
func (s *SenecClient) decodeUint16(hexStr string) (uint16, error) {
	if hexStr == "" {
		return 0, nil
	}

	val, err := strconv.ParseUint(hexStr, 16, 16)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid u16 hex: %s\n", hexStr)
		return 0, nil
	}

	return uint16(val), nil
}

// decodeUint32 decodes an unsigned 32-bit integer
func (s *SenecClient) decodeUint32(hexStr string) (uint32, error) {
	if hexStr == "" {
		return 0, nil
	}

	val, err := strconv.ParseUint(hexStr, 16, 32)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid u32 hex: %s\n", hexStr)
		return 0, nil
	}

	return uint32(val), nil
}

// decodeInt8 decodes a signed 8-bit integer
func (s *SenecClient) decodeInt8(hexStr string) (int8, error) {
	if hexStr == "" {
		return 0, nil
	}

	val, err := strconv.ParseUint(hexStr, 16, 8)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid i8 hex: %s\n", hexStr)
		return 0, nil
	}

	return int8(val), nil
}

// decodeInt16 decodes a signed 16-bit integer
func (s *SenecClient) decodeInt16(hexStr string) (int16, error) {
	if hexStr == "" {
		return 0, nil
	}

	val, err := strconv.ParseUint(hexStr, 16, 16)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid i16 hex: %s\n", hexStr)
		return 0, nil
	}

	return int16(val), nil
}

// decodeInt32 decodes a signed 32-bit integer
func (s *SenecClient) decodeInt32(hexStr string) (int32, error) {
	if hexStr == "" {
		return 0, nil
	}

	val, err := strconv.ParseUint(hexStr, 16, 32)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid i32 hex: %s\n", hexStr)
		return 0, nil
	}

	return int32(val), nil
}

// decodeString decodes a string value
func (s *SenecClient) decodeString(hexStr string) (string, error) {
	if hexStr == "" {
		return "", nil
	}

	// Ensure even number of characters for hex decoding
	if len(hexStr)%2 != 0 {
		hexStr = "0" + hexStr
	}

	// Convert hex string to bytes
	bytes, err := hex.DecodeString(hexStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error decoding hex string '%s': %v\n", hexStr, err)
		return fmt.Sprintf("HEX_ERROR(%s)", hexStr), nil
	}

	// Find null terminator if present
	end := len(bytes)
	for i, b := range bytes {
		if b == 0 {
			end = i
			break
		}
	}

	// Convert bytes to string
	return string(bytes[:end]), nil
}

// HTTP server handler
func handleRequest(w http.ResponseWriter, r *http.Request, client *SenecClient) {
	// Get data from SENEC system (with caching)
	data, err := client.GetData()
	if err != nil {
		http.Error(w, fmt.Sprintf("Error retrieving SENEC data: %v", err), http.StatusInternalServerError)
		return
	}

	// Convert data to JSON string
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		http.Error(w, fmt.Sprintf("Error marshaling JSON: %v", err), http.StatusInternalServerError)
		return
	}

	// Set content type and write response
	w.Header().Set("Content-Type", "application/json")
	w.Write(jsonData)
}

func main() {
	// Parse command line arguments
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <SENEC_IP> [SERVER_IP] [SERVER_PORT]\n", os.Args[0])
		os.Exit(1)
	}

	senecIP := os.Args[1]
	serverIP := "127.0.0.1"
	serverPort := 8080

	if len(os.Args) > 2 {
		serverIP = os.Args[2]
	}

	if len(os.Args) > 3 {
		port, err := strconv.Atoi(os.Args[3])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Invalid port number: %s\n", os.Args[3])
			os.Exit(1)
		}
		serverPort = port
	}

	// Create SENEC client
	client := NewSenecClient(senecIP)

	// Create server
	server := &http.Server{
		Addr: fmt.Sprintf("%s:%d", serverIP, serverPort),
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handleRequest(w, r, client)
		}),
	}

	// Start server in a goroutine
	go func() {
		fmt.Fprintf(os.Stderr, "Starting SENEC data server on http://%s:%d\n", serverIP, serverPort)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		}
	}()

	data, err := client.GetData()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Print formatted JSON to stdout
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error marshaling JSON: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(jsonData))

	// Set up graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)

	// Wait for interrupt signal
	fmt.Fprintf(os.Stderr, "Server is running. Press Ctrl+C to stop.\n")
	<-stop

	// Shutdown the server
	fmt.Fprintf(os.Stderr, "Shutting down\n")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Server shutdown error: %v\n", err)
	}
}
