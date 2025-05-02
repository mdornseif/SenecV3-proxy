package main

import (
    "bytes"
    "crypto/tls"
    "encoding/hex"
    "encoding/json"
    "flag"
    "fmt"
    "io/ioutil"
    "log"
    "math"
    "net/http"
    "os"
    "strconv"
    "strings"
    "sync"
    "time"
)

// StateDecoder maps numeric state values to human-readable descriptions
var StateDecoder = map[float64]string{
    0:  "INITIALSTATE",
    1:  "ERROR INVERTER COMMUNICATION",
    2:  "ERROR ELECTRICY METER",
    3:  "RIPPLE CONTROL RECEIVER",
    4:  "INITIAL CHARGE",
    5:  "MAINTENANCE CHARGE",
    6:  "MAINTENANCE READY",
    7:  "MAINTENANCE REQUIRED",
    8:  "MAN. SAFETY CHARGE",
    9:  "SAFETY CHARGE READY",
    10: "FULL CHARGE",
    11: "EQUALIZATION: CHARGE",
    12: "DESULFATATION: CHARGE",
    13: "BATTERY FULL",
    14: "CHARGE",
    15: "BATTERY EMPTY",
    16: "DISCHARGE",
    17: "PV + DISCHARGE",
    18: "GRID + DISCHARGE",
    19: "PASSIVE",
    20: "OFF",
    21: "OWN CONSUMPTION",
    22: "RESTART",
    23: "MAN. EQUALIZATION: CHARGE",
    24: "MAN. DESULFATATION: CHARGE",
    25: "SAFETY CHARGE",
    26: "BATTERY PROTECTION MODE",
    27: "EG ERROR",
    28: "EG CHARGE",
    29: "EG DISCHARGE",
    30: "EG PASSIVE",
    31: "EG PROHIBIT CHARGE",
    32: "EG PROHIBIT DISCHARGE",
    33: "EMERGENCY CHARGE",
    34: "SOFTWARE UPDATE",
    35: "NSP ERROR",
    36: "NSP ERROR: GRID",
    37: "NSP ERROR: HARDWRE",
    38: "NO SERVER CONNECTION",
    39: "BMS ERROR",
    40: "MAINTENANCE: FILTER",
    41: "BMS SHUTDOWN",
    42: "WAITING EXCESS",
    43: "CAPACITY TEST: CHARGE",
    44: "CAPACITY TEST: DISCHARGE",
    45: "MAN. DESULFATATION: WAIT",
    46: "MAN. DESULFATATION: READY",
    47: "MAN. DESULFATATION: ERROR",
    48: "EQUALIZATION: WAIT",
    49: "EMERGENCY CHARGE: ERROR",
    50: "MAN. EQUALIZATION: WAIT",
    51: "MAN. EQUALIZATION: ERROR",
    52: "MAN: EQUALIZATION: READY",
    53: "AUTO. DESULFATATION: WAIT",
    54: "ABSORPTION PHASE",
    55: "DC-SWITCH OFF",
    56: "PEAK-SHAVING: WAIT",
    57: "ERROR BATTERY INVERTER",
    58: "NPU-ERROR",
    59: "BMS OFFLINE",
    60: "MAINTENANCE CHARGE ERROR",
    61: "MAN. SAFETY CHARGE ERROR",
    62: "SAFETY CHARGE ERROR",
    63: "NO CONNECTION TO MASTER",
    64: "LITHIUM SAFE MODE ACTIVE",
    65: "LITHIUM SAFE MODE DONE",
    66: "BATTERY VOLTAGE ERROR",
    67: "BMS DC SWITCHED OFF",
    68: "GRID INITIALIZATION",
    69: "GRID STABILIZATION",
    70: "REMOTE SHUTDOWN",
    71: "OFFPEAK-CHARGE",
    72: "ERROR HALFBRIDGE",
    73: "BMS: ERROR OPERATING TEMPERATURE",
    74: "FACTORY SETTINGS NOT FOUND",
    75: "BACKUP POWER MODE - ACTIVE",
    76: "BACKUP POWER MODE - BATTERY EMPTY",
    77: "BACKUP POWER MODE ERROR",
    78: "INITIALISING",
    79: "INSTALLATION MODE",
    80: "GRID OFFLINE",
    81: "BMS UPDATE NEEDED",
    82: "BMS CONFIGURATION NEEDED",
    83: "INSULATION TEST",
    84: "SELFTEST",
    85: "EXTERNAL CONTROL",
    86: "ERROR: TEMPERATURESENSOR",
    87: "GRID OPERATOR: CHARGE PROHIBITED",
    88: "GRID OPERATOR: DISCHARGE PROHIBITED",
    89: "SPARE CAPACITY",
    90: "SELFTEST ERROR",
    91: "EARTH FAULT",
    92: "PV-MODE",
    93: "REMOTE DISCONNECTION",
    94: "ERROR DRM0",
    95: "BATTERY DIAGNOSIS",
    96: "BALANCING",
    97: "SAFETY DISCHARGE",
    98: "BMS ERROR - MODULE IMBALANCE",
    99: "WAKE UP CHARGING",
}

// SenecClient represents a client for the SENEC.Home system
type SenecClient struct {
    BaseURL      string
    Client       *http.Client
    CachedData   map[string]map[string]interface{}
    CacheTime    time.Time
    RequestMutex sync.Mutex
}

// Initialize a logger that writes to stderr
var logger = log.New(os.Stderr, "", log.LstdFlags)

// NewSenecClient creates a new SENEC client with the given IP address
func NewSenecClient(ipAddress string) *SenecClient {
    // Create HTTP client with TLS configuration that skips certificate verification
    tr := &http.Transport{
        TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
    }
    client := &http.Client{
        Transport: tr,
        Timeout:   10 * time.Second,
    }

    return &SenecClient{
        BaseURL:    fmt.Sprintf("https://%s/lala.cgi", ipAddress),
        Client:     client,
        CachedData: nil,
    }
}

// GetData retrieves data from the SENEC system with caching and request throttling
func (s *SenecClient) GetData() (map[string]map[string]interface{}, error) {
    // Use mutex to prevent concurrent requests to the SENEC device
    s.RequestMutex.Lock()
    defer s.RequestMutex.Unlock()

    // Check if we have valid cached data (less than 30 seconds old)
    if s.CachedData != nil && time.Since(s.CacheTime) < 30*time.Second {
        return s.CachedData, nil
    }

    // Try to fetch fresh data from the SENEC device
    data, err := s.fetchDataFromDevice()
    if err != nil {
        // If we have cached data, use it as fallback
        if s.CachedData != nil {
            logger.Printf("Error fetching data, using cached data: %v", err)
            return s.CachedData, nil
        }
        return nil, err
    }

    // Update cache with fresh data
    s.CachedData = data
    s.CacheTime = time.Now()

    return data, nil
}

// fetchDataFromDevice fetches fresh data from the SENEC device
func (s *SenecClient) fetchDataFromDevice() (map[string]map[string]interface{}, error) {
    // Create the payload structure with all requested keys
    payload := map[string]map[string]string{
        "PM1OBJ1": {
            "FREQ":    "",
            "U_AC":    "",
            "I_AC":    "",
            "P_AC":    "",
            "P_TOTAL": "",
        },
        "PM1OBJ2": {
            "FREQ":    "",
            "U_AC":    "",
            "I_AC":    "",
            "P_AC":    "",
            "P_TOTAL": "",
        },
        "ENERGY": {
            "STAT_STATE":               "",
            "STAT_HOURS_OF_OPERATION":  "",
            "GUI_BAT_DATA_POWER":       "",
            "GUI_BAT_DATA_VOLTAGE":     "",
            "GUI_BAT_DATA_CURRENT":     "",
            "GUI_BAT_DATA_FUEL_CHARGE": "",
            "GUI_HOUSE_POW":            "",
            "GUI_INVERTER_POWER":       "",
            "STAT_LIMITED_NET_SKEW":    "",
        },
        "SYS_UPDATE": {
            "NPU_VER":           "",
            "NPU_IMAGE_VERSION": "",
        },
        // Additional keys as requested
        "PV1": {
            "POWER_RATIO":         "",
            "POWER_RATIO_L1":      "",
            "POWER_RATIO_L2":      "",
            "POWER_RATIO_L3":      "",
            "MPP_VOL":             "",
            "MPP_CUR":             "",
            "MPP_POWER":           "",
            "MPP_AVAIL":           "",
            "INTERNAL_MD_AVAIL":   "",
            "INTERNAL_MD_MODEL":   "",
            "INTERNAL_MD_VERSION": "",
        },
        "WIZARD": {
            "MAC_ADDRESS_BYTES": "",
        },
        "BAT1OBJ1": {
            "TEMP1":       "",
            "TEMP2":       "",
            "TEMP3":       "",
            "TEMP4":       "",
            "TEMP5":       "",
            "S":           "",
            "P":           "",
            "Q":           "",
            "SW_VERSION":  "",
            "SW_VERSION2": "",
            "SW_VERSION3": "",
            "I_DC":        "",
        },
        "PWR_UNIT": {
            "POWER_L1": "",
            "POWER_L2": "",
            "POWER_L3": "",
        },
        "BAT1": {
            "CEI_LIMIT":      "",
            "SPARE_CAPACITY": "",
        },
        "BMS": {
            "NR_INSTALLED": "",
        },
        "WALLBOX": {},
        "GRIDCONFIG": {},
    }

    // Convert payload to JSON
    payloadBytes, err := json.Marshal(payload)
    if err != nil {
        return nil, fmt.Errorf("error marshaling payload: %v", err)
    }

    // Create request
    req, err := http.NewRequest("POST", s.BaseURL, bytes.NewBuffer(payloadBytes))
    if err != nil {
        return nil, fmt.Errorf("error creating request: %v", err)
    }
    req.Header.Set("Content-Type", "application/json")

    // Send request
    resp, err := s.Client.Do(req)
    if err != nil {
        return nil, fmt.Errorf("error sending request: %v", err)
    }
    defer resp.Body.Close()

    // Check response status
    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("received non-OK response: %s", resp.Status)
    }

    // Read response body
    body, err := ioutil.ReadAll(resp.Body)
    if err != nil {
        return nil, fmt.Errorf("error reading response body: %v", err)
    }

    // Parse response JSON
    var responseData map[string]map[string]interface{}
    if err := json.Unmarshal(body, &responseData); err != nil {
        return nil, fmt.Errorf("error parsing response JSON: %v", err)
    }

    // Decode the SENEC-specific encoding
    decodedData := s.decodeResponse(responseData)

    // Add decoded state information
    s.addDecodedState(decodedData)

    return decodedData, nil
}

// addDecodedState adds a human-readable state description for ENERGY.STAT_STATE
func (s *SenecClient) addDecodedState(data map[string]map[string]interface{}) {
    // Check if ENERGY category and STAT_STATE field exist
    if energyData, exists := data["ENERGY"]; exists {
        if stateValue, exists := energyData["STAT_STATE"]; exists {
            // Try to convert the state value to a float64 for lookup
            if stateFloat, ok := stateValue.(float64); ok {
                // Look up the state description
                if stateDesc, exists := StateDecoder[stateFloat]; exists {
                    // Add the decoded state to the data
                    energyData["STAT_STATE_DECODED"] = stateDesc
                } else {
                    // If state is not in our map, provide a generic message
                    energyData["STAT_STATE_DECODED"] = fmt.Sprintf("UNKNOWN STATE (%v)", stateFloat)
                }
            } else if stateInt, ok := stateValue.(int); ok {
                // Handle integer state values
                stateFloat := float64(stateInt)
                if stateDesc, exists := StateDecoder[stateFloat]; exists {
                    energyData["STAT_STATE_DECODED"] = stateDesc
                } else {
                    energyData["STAT_STATE_DECODED"] = fmt.Sprintf("UNKNOWN STATE (%v)", stateInt)
                }
            } else if stateUint8, ok := stateValue.(uint8); ok {
                // Handle uint8 state values
                stateFloat := float64(stateUint8)
                if stateDesc, exists := StateDecoder[stateFloat]; exists {
                    energyData["STAT_STATE_DECODED"] = stateDesc
                } else {
                    energyData["STAT_STATE_DECODED"] = fmt.Sprintf("UNKNOWN STATE (%v)", stateUint8)
                }
            } else {
                // If state is not a number, provide the raw value
                energyData["STAT_STATE_DECODED"] = fmt.Sprintf("INVALID STATE FORMAT (%v)", stateValue)
            }
        }
    }
}

// decodeResponse decodes the SENEC-specific encoding in the response
func (s *SenecClient) decodeResponse(data map[string]map[string]interface{}) map[string]map[string]interface{} {
    decodedData := make(map[string]map[string]interface{})

    for category, values := range data {
        decodedData[category] = make(map[string]interface{})
        for key, value := range values {
            // Automatically detect and decode any value, including arrays
            decodedData[category][key] = decodeAnyValue(value)
        }
    }

    return decodedData
}

// decodeAnyValue automatically detects and decodes any value type, including arrays
func decodeAnyValue(value interface{}) interface{} {
    // Check if the value is a slice/array
    if arrayValue, ok := value.([]interface{}); ok {
        // Create a new array to hold decoded values
        decodedArray := make([]interface{}, len(arrayValue))

        // Decode each element in the array
        for i, element := range arrayValue {
            decodedArray[i] = decodeAnyValue(element)
        }

        return decodedArray
    } else if mapValue, ok := value.(map[string]interface{}); ok {
        // Handle nested maps (objects)
        decodedMap := make(map[string]interface{})
        for k, v := range mapValue {
            decodedMap[k] = decodeAnyValue(v)
        }
        return decodedMap
    } else {
        // If it's not an array or map, decode it as a single value
        return decodeValue(value)
    }
}

// decodeValue decodes a single value based on its type and prefix
func decodeValue(value interface{}) interface{} {
    // Handle string values with special prefixes
    if strValue, ok := value.(string); ok {
        // Check for different prefixes based on pysenec implementation
        if strings.HasPrefix(strValue, "fl_") {
            // Floating point value (fl_)
            return decodeFloat(strValue[3:])
        } else if strings.HasPrefix(strValue, "u8_") {
            // Unsigned 8-bit integer (u8_)
            return decodeUint8(strValue[3:])
        } else if strings.HasPrefix(strValue, "u1_") {
            // Unsigned 16-bit integer (u1_)
            return decodeUint16(strValue[3:])
        } else if strings.HasPrefix(strValue, "u3_") {
            // Unsigned 32-bit integer (u3_)
            return decodeUint32(strValue[3:])
        } else if strings.HasPrefix(strValue, "i1_") {
            // Signed 16-bit integer (i1_)
            return decodeInt16(strValue[3:])
        } else if strings.HasPrefix(strValue, "i3_") {
            // Signed 32-bit integer (i3_)
            return decodeInt32(strValue[3:])
        } else if strings.HasPrefix(strValue, "i8_") {
            // Signed 8-bit integer (i8_)
            return decodeInt8(strValue[3:])
        } else if strings.HasPrefix(strValue, "st_") {
            // String value (st_)
            return decodeString(strValue[3:])
        }
    }

    // Return the value as is if it doesn't match any known format
    return value
}

// decodeFloat decodes a hexadecimal string to a float64 value
func decodeFloat(hexStr string) float64 {
    // Handle empty string
    if hexStr == "" {
        return 0
    }

    // Convert hex string to uint64
    bits, err := strconv.ParseUint(hexStr, 16, 32)
    if err != nil {
        return 0
    }

    // Convert bits to float32 and then to float64
    // This matches the IEEE 754 conversion used in pysenec
    return float64(math.Float32frombits(uint32(bits)))
}

// decodeUint8 decodes a hexadecimal string to a uint8 value
func decodeUint8(hexStr string) uint8 {
    if hexStr == "" {
        return 0
    }
    val, err := strconv.ParseUint(hexStr, 16, 8)
    if err != nil {
        return 0
    }
    return uint8(val)
}

// decodeUint16 decodes a hexadecimal string to a uint16 value
func decodeUint16(hexStr string) uint16 {
    if hexStr == "" {
        return 0
    }
    val, err := strconv.ParseUint(hexStr, 16, 16)
    if err != nil {
        return 0
    }
    return uint16(val)
}

// decodeUint32 decodes a hexadecimal string to a uint32 value
func decodeUint32(hexStr string) uint32 {
    if hexStr == "" {
        return 0
    }
    val, err := strconv.ParseUint(hexStr, 16, 32)
    if err != nil {
        return 0
    }
    return uint32(val)
}

// decodeInt8 decodes a hexadecimal string to an int8 value
func decodeInt8(hexStr string) int8 {
    if hexStr == "" {
        return 0
    }
    val, err := strconv.ParseUint(hexStr, 16, 8)
    if err != nil {
        return 0
    }
    return int8(val)
}

// decodeInt16 decodes a hexadecimal string to an int16 value
func decodeInt16(hexStr string) int16 {
    if hexStr == "" {
        return 0
    }
    val, err := strconv.ParseUint(hexStr, 16, 16)
    if err != nil {
        return 0
    }
    return int16(val)
}

// decodeInt32 decodes a hexadecimal string to an int32 value
func decodeInt32(hexStr string) int32 {
    if hexStr == "" {
        return 0
    }
    val, err := strconv.ParseUint(hexStr, 16, 32)
    if err != nil {
        return 0
    }
    return int32(val)
}

// decodeString decodes a hexadecimal string to a UTF-8 string
func decodeString(hexStr string) string {
    if hexStr == "" {
        return ""
    }

    // Convert hex string to bytes
    bytes, err := hex.DecodeString(hexStr)
    if err != nil {
        return hexStr
    }

    // Convert bytes to string, stopping at null terminator if present
    for i, b := range bytes {
        if b == 0 {
            return string(bytes[:i])
        }
    }
    return string(bytes)
}

// PrintFormattedData prints the data as formatted JSON to stdout
func (s *SenecClient) PrintFormattedData(data map[string]map[string]interface{}) {
    // Marshal data to JSON with indentation for readability
    jsonData, err := json.MarshalIndent(data, "", "  ")
    if err != nil {
        logger.Printf("Error encoding JSON: %v\n", err)
        return
    }

    // Print the JSON data to stdout
    fmt.Println(string(jsonData))
}

// startServer starts an HTTP server that serves SENEC data as JSON
func startServer(client *SenecClient, port int) {
    // Define handler for the root path - returns SENEC data
    http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        // Get data from SENEC system (with caching)
        data, err := client.GetData()
        if err != nil {
            http.Error(w, fmt.Sprintf("Error retrieving SENEC data: %v", err), http.StatusInternalServerError)
            return
        }

        // Set content type to JSON
        w.Header().Set("Content-Type", "application/json")

        // Marshal data to JSON with indentation for readability
        jsonData, err := json.MarshalIndent(data, "", "  ")
        if err != nil {
            http.Error(w, fmt.Sprintf("Error encoding JSON: %v", err), http.StatusInternalServerError)
            return
        }

        // Write JSON response
        w.Write(jsonData)
    })

    // Start the server in a goroutine so it doesn't block
    go func() {
        serverAddr := fmt.Sprintf(":%d", port)
        logger.Printf("Starting SENEC data server on http://localhost%s", serverAddr)
        if err := http.ListenAndServe(serverAddr, nil); err != nil {
            logger.Fatalf("Server error: %v", err)
        }
    }()
}

func main() {
    // Define command line flags
    ipAddress := flag.String("ip", "192.168.18.24", "IP address of the SENEC system")
    noServer := flag.Bool("no-server", false, "Do not run HTTP server")
    port := flag.Int("port", 8080, "Port for HTTP server mode")

    // Parse command line flags
    flag.Parse()

    // Create a new client with the specified IP address
    client := NewSenecClient(*ipAddress)

    // Start the server by default unless --no-server is specified
    if !*noServer {
        startServer(client, *port)
    }

    // Always get and print data to stdout
    data, err := client.GetData()
    if err != nil {
        logger.Printf("Error: %v\n", err)
        os.Exit(1)
    }

    // Print the formatted data to stdout
    client.PrintFormattedData(data)

    // If server is running, keep the program alive
    if !*noServer {
        logger.Printf("Server is running. Press Ctrl+C to stop.")
        // Wait indefinitely
        select {}
    }
}
