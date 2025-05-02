use std::collections::HashMap;
use std::env;
use std::net::SocketAddr;
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use hyper::client::HttpConnector;
use hyper::service::{make_service_fn, service_fn};
use hyper::{Body, Client as HyperClient, Method, Request, Response, Server, StatusCode};
use hyper_rustls::{HttpsConnector, HttpsConnectorBuilder};
use rustls::{Certificate, ClientConfig, RootCertStore, ServerName};
use rustls::client::{ServerCertVerified, ServerCertVerifier};
use serde_json::{json, Value};

// Custom certificate verifier that accepts all certificates
struct AcceptAllCertificateVerifier {}

impl ServerCertVerifier for AcceptAllCertificateVerifier {
    fn verify_server_cert(
        &self,
        _end_entity: &Certificate,
        _intermediates: &[Certificate],
        _server_name: &ServerName,
        _scts: &mut dyn Iterator<Item = &[u8]>,
        _ocsp_response: &[u8],
        _now: std::time::SystemTime,
    ) -> Result<ServerCertVerified, rustls::Error> {
        // Accept any certificate
        Ok(ServerCertVerified::assertion())
    }
}

// State decoder mapping
lazy_static::lazy_static! {
    static ref STATE_DECODER: HashMap<u8, &'static str> = {
        let mut m = HashMap::new();
        m.insert(0, "INITIALSTATE");
        m.insert(1, "ERROR INVERTER COMMUNICATION");
        m.insert(2, "ERROR ELECTRICY METER");
        m.insert(3, "RIPPLE CONTROL RECEIVER");
        m.insert(4, "INITIAL CHARGE");
        m.insert(5, "MAINTENANCE CHARGE");
        m.insert(6, "MAINTENANCE READY");
        m.insert(7, "MAINTENANCE REQUIRED");
        m.insert(8, "MAN. SAFETY CHARGE");
        m.insert(9, "SAFETY CHARGE READY");
        m.insert(10, "FULL CHARGE");
        m.insert(11, "EQUALIZATION: CHARGE");
        m.insert(12, "DESULFATATION: CHARGE");
        m.insert(13, "BATTERY FULL");
        m.insert(14, "CHARGE");
        m.insert(15, "BATTERY EMPTY");
        m.insert(16, "DISCHARGE");
        m.insert(17, "PV + DISCHARGE");
        m.insert(18, "GRID + DISCHARGE");
        m.insert(19, "PASSIVE");
        m.insert(20, "OFF");
        m.insert(21, "OWN CONSUMPTION");
        m.insert(22, "RESTART");
        m.insert(23, "MAN. EQUALIZATION: CHARGE");
        m.insert(24, "MAN. DESULFATATION: CHARGE");
        m.insert(25, "SAFETY CHARGE");
        m.insert(26, "BATTERY PROTECTION MODE");
        m.insert(27, "EG ERROR");
        m.insert(28, "EG CHARGE");
        m.insert(29, "EG DISCHARGE");
        m.insert(30, "EG PASSIVE");
        m.insert(31, "EG PROHIBIT CHARGE");
        m.insert(32, "EG PROHIBIT DISCHARGE");
        m.insert(33, "EMERGENCY CHARGE");
        m.insert(34, "SOFTWARE UPDATE");
        m.insert(35, "NSP ERROR");
        m.insert(36, "NSP ERROR: GRID");
        m.insert(37, "NSP ERROR: HARDWRE");
        m.insert(38, "NO SERVER CONNECTION");
        m.insert(39, "BMS ERROR");
        m.insert(40, "MAINTENANCE: FILTER");
        m.insert(41, "BMS SHUTDOWN");
        m.insert(42, "WAITING EXCESS");
        m.insert(43, "CAPACITY TEST: CHARGE");
        m.insert(44, "CAPACITY TEST: DISCHARGE");
        m.insert(45, "MAN. DESULFATATION: WAIT");
        m.insert(46, "MAN. DESULFATATION: READY");
        m.insert(47, "MAN. DESULFATATION: ERROR");
        m.insert(48, "EQUALIZATION: WAIT");
        m.insert(49, "EMERGENCY CHARGE: ERROR");
        m.insert(50, "MAN. EQUALIZATION: WAIT");
        m.insert(51, "MAN. EQUALIZATION: ERROR");
        m.insert(52, "MAN: EQUALIZATION: READY");
        m.insert(53, "AUTO. DESULFATATION: WAIT");
        m.insert(54, "ABSORPTION PHASE");
        m.insert(55, "DC-SWITCH OFF");
        m.insert(56, "PEAK-SHAVING: WAIT");
        m.insert(57, "ERROR BATTERY INVERTER");
        m.insert(58, "NPU-ERROR");
        m.insert(59, "BMS OFFLINE");
        m.insert(60, "MAINTENANCE CHARGE ERROR");
        m.insert(61, "MAN. SAFETY CHARGE ERROR");
        m.insert(62, "SAFETY CHARGE ERROR");
        m.insert(63, "NO CONNECTION TO MASTER");
        m.insert(64, "LITHIUM SAFE MODE ACTIVE");
        m.insert(65, "LITHIUM SAFE MODE DONE");
        m.insert(66, "BATTERY VOLTAGE ERROR");
        m.insert(67, "BMS DC SWITCHED OFF");
        m.insert(68, "GRID INITIALIZATION");
        m.insert(69, "GRID STABILIZATION");
        m.insert(70, "REMOTE SHUTDOWN");
        m.insert(71, "OFFPEAK-CHARGE");
        m.insert(72, "ERROR HALFBRIDGE");
        m.insert(73, "BMS: ERROR OPERATING TEMPERATURE");
        m.insert(74, "FACTORY SETTINGS NOT FOUND");
        m.insert(75, "BACKUP POWER MODE - ACTIVE");
        m.insert(76, "BACKUP POWER MODE - BATTERY EMPTY");
        m.insert(77, "BACKUP POWER MODE ERROR");
        m.insert(78, "INITIALISING");
        m.insert(79, "INSTALLATION MODE");
        m.insert(80, "GRID OFFLINE");
        m.insert(81, "BMS UPDATE NEEDED");
        m.insert(82, "BMS CONFIGURATION NEEDED");
        m.insert(83, "INSULATION TEST");
        m.insert(84, "SELFTEST");
        m.insert(85, "EXTERNAL CONTROL");
        m.insert(86, "ERROR: TEMPERATURESENSOR");
        m.insert(87, "GRID OPERATOR: CHARGE PROHIBITED");
        m.insert(88, "GRID OPERATOR: DISCHARGE PROHIBITED");
        m.insert(89, "SPARE CAPACITY");
        m.insert(90, "SELFTEST ERROR");
        m.insert(91, "EARTH FAULT");
        m.insert(92, "PV-MODE");
        m.insert(93, "REMOTE DISCONNECTION");
        m.insert(94, "ERROR DRM0");
        m.insert(95, "BATTERY DIAGNOSIS");
        m.insert(96, "BALANCING");
        m.insert(97, "SAFETY DISCHARGE");
        m.insert(98, "BMS ERROR - MODULE IMBALANCE");
        m.insert(99, "WAKE UP CHARGING");
        m
    };
}

// Cache structure
struct Cache {
    data: Option<Value>,
    timestamp: Option<Instant>,
}

impl Cache {
    fn new() -> Self {
        Self {
            data: None,
            timestamp: None,
        }
    }

    fn is_valid(&self) -> bool {
        match self.timestamp {
            Some(time) => time.elapsed() < Duration::from_secs(30),
            None => false,
        }
    }

    fn set(&mut self, data: Value) {
        self.data = Some(data);
        self.timestamp = Some(Instant::now());
    }

    fn get(&self) -> Option<&Value> {
        self.data.as_ref()
    }
}

// SENEC client
struct SenecClient {
    base_url: String,
    client: HyperClient<HttpsConnector<HttpConnector>>,
    cache: Arc<Mutex<Cache>>,
}

impl SenecClient {
    fn new(ip_address: &str) -> Self {
        // Create a custom TLS config that accepts all certificates
        let mut root_store = RootCertStore::empty();

        // Create a client config with custom certificate verifier
        let mut client_config = ClientConfig::builder()
            .with_safe_defaults()
            .with_root_certificates(root_store)
            .with_no_client_auth();

        // Set the custom certificate verifier
        let verifier = Arc::new(AcceptAllCertificateVerifier {});
        client_config.dangerous().set_certificate_verifier(verifier);

        // Create HTTPS connector with custom TLS config
        let https = HttpsConnectorBuilder::new()
            .with_tls_config(client_config)
            .https_only()
            .enable_http1()
            .build();

        // Create HTTP client
        let client = HyperClient::builder().build::<_, Body>(https);

        Self {
            base_url: format!("https://{}/lala.cgi", ip_address),
            client,
            cache: Arc::new(Mutex::new(Cache::new())),
        }
    }

    async fn get_data(&self) -> Result<Value, Box<dyn std::error::Error + Send + Sync>> {
        // Check cache first
        {
            let cache = self.cache.lock().unwrap();
            if cache.is_valid() {
                if let Some(data) = cache.get() {
                    return Ok(data.clone());
                }
            }
        }

        // Create payload for SENEC API
        let payload = json!({
            "PM1OBJ1": {
                "FREQ": "",
                "U_AC": "",
                "I_AC": "",
                "P_AC": "",
                "P_TOTAL": ""
            },
            "ENERGY": {
                "STAT_STATE": "",
                "STAT_HOURS_OF_OPERATION": "",
                "GUI_BAT_DATA_POWER": "",
                "GUI_BAT_DATA_VOLTAGE": "",
                "GUI_BAT_DATA_CURRENT": "",
                "GUI_BAT_DATA_FUEL_CHARGE": "",
                "GUI_HOUSE_POW": "",
                "GUI_INVERTER_POWER": "",
            },
            "PV1": {
                "POWER_RATIO": "",
                "POWER_RATIO_L1": "",
                "POWER_RATIO_L2": "",
                "POWER_RATIO_L3": "",
                "MPP_VOL": "",
                "MPP_CUR": "",
                "MPP_POWER": "",
                "MPP_AVAIL": "",
            },
            "BAT1OBJ1": {
                "TEMP1": "",
                "TEMP2": "",
                "S": "",
                "P": "",
                "Q": "",
                "SW_VERSION": "",
                "SW_VERSION2": "",
                "SW_VERSION3": "",
                "I_DC": ""
            },
            "BAT1": {
                "CEI_LIMIT": "",
                "SPARE_CAPACITY": ""
            },
        });

        // Try to fetch data from SENEC device
        let result = self.fetch_data_from_device(&payload).await;

        match result {
            Ok(data) => {
                // Update cache
                let mut cache = self.cache.lock().unwrap();
                cache.set(data.clone());
                Ok(data)
            }
            Err(e) => {
                // Try to use cached data if available
                let cache = self.cache.lock().unwrap();
                if let Some(data) = cache.get() {
                    eprintln!("Error fetching data, using cached data: {}", e);
                    Ok(data.clone())
                } else {
                    Err(e)
                }
            }
        }
    }

    async fn fetch_data_from_device(
        &self,
        payload: &Value,
    ) -> Result<Value, Box<dyn std::error::Error + Send + Sync>> {
        // Convert payload to JSON string
        let json_payload = serde_json::to_string(payload)?;

        // Create request
        let req = Request::builder()
            .method(Method::POST)
            .uri(&self.base_url)
            .header("Content-Type", "application/json")
            .body(Body::from(json_payload))?;

        // Send request
        let resp = self.client.request(req).await?;

        // Check response status
        if !resp.status().is_success() {
            return Err(format!("Received non-OK response: {}", resp.status()).into());
        }

        // Read response body
        let body_bytes = hyper::body::to_bytes(resp.into_body()).await?;
        let body_str = String::from_utf8(body_bytes.to_vec())?;

        // Parse response JSON
        let response_data: Value = serde_json::from_str(&body_str)?;

        // Decode the SENEC-specific encoding
        let decoded_data = self.decode_response(&response_data)?;

        // Add decoded state information
        let decoded_data_with_state = self.add_decoded_state(decoded_data)?;

        Ok(decoded_data_with_state)
    }

    fn decode_response(&self, data: &Value) -> Result<Value, Box<dyn std::error::Error + Send + Sync>> {
        let mut decoded_data = json!({});

        if let Value::Object(obj) = data {
            for (category, values) in obj {
                if let Value::Object(values_obj) = values {
                    let mut decoded_category = json!({});

                    for (key, value) in values_obj {
                        let decoded_value = self.decode_any_value(value)?;
                        decoded_category[key] = decoded_value;
                    }

                    decoded_data[category] = decoded_category;
                }
            }
        }

        Ok(decoded_data)
    }

    fn decode_any_value(&self, value: &Value) -> Result<Value, Box<dyn std::error::Error + Send + Sync>> {
        match value {
            Value::Array(array) => {
                let mut decoded_array = Vec::new();
                for item in array {
                    decoded_array.push(self.decode_any_value(item)?);
                }
                Ok(Value::Array(decoded_array))
            }
            Value::Object(obj) => {
                let mut decoded_obj = serde_json::Map::new();
                for (k, v) in obj {
                    decoded_obj.insert(k.clone(), self.decode_any_value(v)?);
                }
                Ok(Value::Object(decoded_obj))
            }
            Value::String(s) => Ok(self.decode_value(s)?),
            _ => Ok(value.clone()),
        }
    }

    fn decode_value(&self, value: &str) -> Result<Value, Box<dyn std::error::Error + Send + Sync>> {
        if value.starts_with("fl_") {
            // Floating point value
            Ok(Value::Number(serde_json::Number::from_f64(
                self.decode_float(&value[3..])?,
            ).unwrap_or(serde_json::Number::from(0))))
        } else if value.starts_with("u8_") {
            // Unsigned 8-bit integer
            Ok(Value::Number(serde_json::Number::from(
                self.decode_uint8(&value[3..])?,
            )))
        } else if value.starts_with("u1_") {
            // Unsigned 16-bit integer
            Ok(Value::Number(serde_json::Number::from(
                self.decode_uint16(&value[3..])?,
            )))
        } else if value.starts_with("u3_") {
            // Unsigned 32-bit integer
            Ok(Value::Number(serde_json::Number::from(
                self.decode_uint32(&value[3..])?,
            )))
        } else if value.starts_with("i1_") {
            // Signed 16-bit integer
            Ok(Value::Number(serde_json::Number::from(
                self.decode_int16(&value[3..])?,
            )))
        } else if value.starts_with("i3_") {
            // Signed 32-bit integer
            Ok(Value::Number(serde_json::Number::from(
                self.decode_int32(&value[3..])?,
            )))
        } else if value.starts_with("i8_") {
            // Signed 8-bit integer
            Ok(Value::Number(serde_json::Number::from(
                self.decode_int8(&value[3..])?,
            )))
        } else if value.starts_with("st_") {
            // String value
            Ok(Value::String(self.decode_string(&value[3..])?))
        } else {
            // Return as is
            Ok(Value::String(value.to_string()))
        }
    }

    fn decode_float(&self, hex_str: &str) -> Result<f64, Box<dyn std::error::Error + Send + Sync>> {
        if hex_str.is_empty() {
            return Ok(0.0);
        }

        // Convert hex string to u32
        match u32::from_str_radix(hex_str, 16) {
            Ok(bits) => {
                // Convert bits to f32 and then to f64
                Ok(f32::from_bits(bits) as f64)
            }
            Err(_) => {
                eprintln!("Invalid float hex: {}", hex_str);
                Ok(0.0) // Return default value on error
            }
        }
    }

    fn decode_uint8(&self, hex_str: &str) -> Result<u8, Box<dyn std::error::Error + Send + Sync>> {
        if hex_str.is_empty() {
            return Ok(0);
        }
        match u8::from_str_radix(hex_str, 16) {
            Ok(val) => Ok(val),
            Err(_) => {
                eprintln!("Invalid u8 hex: {}", hex_str);
                Ok(0) // Return default value on error
            }
        }
    }

    fn decode_uint16(&self, hex_str: &str) -> Result<u16, Box<dyn std::error::Error + Send + Sync>> {
        if hex_str.is_empty() {
            return Ok(0);
        }
        match u16::from_str_radix(hex_str, 16) {
            Ok(val) => Ok(val),
            Err(_) => {
                eprintln!("Invalid u16 hex: {}", hex_str);
                Ok(0) // Return default value on error
            }
        }
    }

    fn decode_uint32(&self, hex_str: &str) -> Result<u32, Box<dyn std::error::Error + Send + Sync>> {
        if hex_str.is_empty() {
            return Ok(0);
        }
        match u32::from_str_radix(hex_str, 16) {
            Ok(val) => Ok(val),
            Err(_) => {
                eprintln!("Invalid u32 hex: {}", hex_str);
                Ok(0) // Return default value on error
            }
        }
    }

    fn decode_int8(&self, hex_str: &str) -> Result<i8, Box<dyn std::error::Error + Send + Sync>> {
        if hex_str.is_empty() {
            return Ok(0);
        }
        match u8::from_str_radix(hex_str, 16) {
            Ok(val) => Ok(val as i8),
            Err(_) => {
                eprintln!("Invalid i8 hex: {}", hex_str);
                Ok(0) // Return default value on error
            }
        }
    }

    fn decode_int16(&self, hex_str: &str) -> Result<i16, Box<dyn std::error::Error + Send + Sync>> {
        if hex_str.is_empty() {
            return Ok(0);
        }
        match u16::from_str_radix(hex_str, 16) {
            Ok(val) => Ok(val as i16),
            Err(_) => {
                eprintln!("Invalid i16 hex: {}", hex_str);
                Ok(0) // Return default value on error
            }
        }
    }

    fn decode_int32(&self, hex_str: &str) -> Result<i32, Box<dyn std::error::Error + Send + Sync>> {
        if hex_str.is_empty() {
            return Ok(0);
        }
        match u32::from_str_radix(hex_str, 16) {
            Ok(val) => Ok(val as i32),
            Err(_) => {
                eprintln!("Invalid i32 hex: {}", hex_str);
                Ok(0) // Return default value on error
            }
        }
    }

    fn decode_string(&self, hex_str: &str) -> Result<String, Box<dyn std::error::Error + Send + Sync>> {
        if hex_str.is_empty() {
            return Ok(String::new());
        }

        // Ensure even number of characters for hex decoding
        let hex_str = if hex_str.len() % 2 != 0 {
            // If odd, pad with a leading zero
            format!("0{}", hex_str)
        } else {
            hex_str.to_string()
        };

        // Convert hex string to bytes
        let bytes = match hex::decode(&hex_str) {
            Ok(b) => b,
            Err(e) => {
                eprintln!("Error decoding hex string '{}': {}", hex_str, e);
                return Ok(format!("HEX_ERROR({})", hex_str));
            }
        };

        // Find null terminator if present
        let end = bytes.iter().position(|&b| b == 0).unwrap_or(bytes.len());

        // Convert bytes to string
        Ok(String::from_utf8_lossy(&bytes[0..end]).to_string())
    }

    fn add_decoded_state(&self, mut data: Value) -> Result<Value, Box<dyn std::error::Error + Send + Sync>> {
        // Check if ENERGY category and STAT_STATE field exist
        if let Some(energy_data) = data.get_mut("ENERGY") {
            if let Some(state_value) = energy_data.get("STAT_STATE") {
                // Try to convert the state value to an integer for lookup
                let state_num = if let Some(num) = state_value.as_u64() {
                    // Ensure it fits in a u8
                    if num <= 255 {
                        num as u8
                    } else {
                        // Handle out of range values
                        energy_data["STAT_STATE_DECODED"] = Value::String(format!("OUT OF RANGE STATE ({})", num));
                        return Ok(data);
                    }
                } else if let Some(num) = state_value.as_f64() {
                    // Convert float to integer if needed
                    if num >= 0.0 && num <= 255.0 && num.fract() == 0.0 {
                        num as u8
                    } else {
                        // Handle invalid float values
                        energy_data["STAT_STATE_DECODED"] = Value::String(format!("INVALID FLOAT STATE ({})", num));
                        return Ok(data);
                    }
                } else {
                    // If state is not a number, provide the raw value
                    energy_data["STAT_STATE_DECODED"] = Value::String(format!("INVALID STATE FORMAT ({:?})", state_value));
                    return Ok(data);
                };

                // Look up the state description
                if let Some(state_desc) = STATE_DECODER.get(&state_num) {
                    // Add the decoded state to the data
                    energy_data["STAT_STATE_DECODED"] = Value::String(state_desc.to_string());
                } else {
                    // If state is not in our map, provide a generic message
                    energy_data["STAT_STATE_DECODED"] = Value::String(format!("UNKNOWN STATE ({})", state_num));
                }
            }
        }

        Ok(data)
    }
}

// HTTP server handler
async fn handle_request(
    client: Arc<SenecClient>,
    _req: Request<Body>,
) -> Result<Response<Body>, hyper::Error> {
    // Get data from SENEC system (with caching)
    match client.get_data().await {
        Ok(data) => {
            // Convert data to JSON string
            let json_data = serde_json::to_string_pretty(&data).unwrap_or_else(|_| "{}".to_string());

            // Create response
            let response = Response::builder()
                .status(StatusCode::OK)
                .header("Content-Type", "application/json")
                .body(Body::from(json_data))
                .unwrap();

            Ok(response)
        }
        Err(e) => {
            // Create error response
            let response = Response::builder()
                .status(StatusCode::INTERNAL_SERVER_ERROR)
                .body(Body::from(format!("Error retrieving SENEC data: {}", e)))
                .unwrap();

            Ok(response)
        }
    }
}

// Simple command line argument parsing
struct Args {
    senec_ip: String,
    server_ip: Option<String>,
    server_port: Option<u16>,
}

impl Args {
    fn parse() -> Result<Self, String> {
        let args: Vec<String> = env::args().collect();

        // Check if we have at least the SENEC IP address
        if args.len() < 2 {
            return Err("Usage: senec_client <SENEC_IP> [SERVER_IP] [SERVER_PORT]".to_string());
        }

        let senec_ip = args[1].clone();
        let server_ip = if args.len() > 2 { Some(args[2].clone()) } else { Some("127.0.0.1".to_string()) };
        let server_port = if args.len() > 3 { 
            match args[3].parse::<u16>() {
                Ok(port) => Some(port),
                Err(_) => return Err(format!("Invalid port number: {}", args[3])),
            }
        } else { 
            Some(8080) 
        };

        Ok(Args {
            senec_ip,
            server_ip,
            server_port,
        })
    }

    fn run_server(&self) -> bool {
        self.server_ip.is_some() && self.server_port.is_some()
    }
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    // Parse command line arguments
    let args = match Args::parse() {
        Ok(args) => args,
        Err(e) => {
            eprintln!("Error: {}", e);
            std::process::exit(1);
        }
    };

    // Create SENEC client
    let client = Arc::new(SenecClient::new(&args.senec_ip));

    // Start HTTP server if server IP and port are provided
    if args.run_server() {
        // Create server address
        let server_ip = args.server_ip.as_ref().unwrap();
        let server_port = args.server_port.unwrap();
        let addr: SocketAddr = format!("{}:{}", server_ip, server_port).parse()?;

        // Create service
        let client_clone = client.clone();
        let make_svc = make_service_fn(move |_conn| {
            let client = client_clone.clone();
            async move {
                Ok::<_, hyper::Error>(service_fn(move |req| {
                    handle_request(client.clone(), req)
                }))
            }
        });

        // Create server
        let server = Server::bind(&addr).serve(make_svc);

        // Log server start
        eprintln!("Starting SENEC data server on http://{}:{}", server_ip, server_port);

        // Start server in a separate task
        tokio::spawn(async move {
            if let Err(e) = server.await {
                eprintln!("Server error: {}", e);
            }
        });
    }

    // Always get and print data to stdout
    match client.get_data().await {
        Ok(data) => {
            // Print formatted JSON to stdout
            println!("{}", serde_json::to_string_pretty(&data)?);
        }
        Err(e) => {
            eprintln!("Error: {}", e);
            std::process::exit(1);
        }
    }

    // If server is running, keep the program alive
    if args.run_server() {
        eprintln!("Server is running. Press Ctrl+C to stop.");
        // Wait for Ctrl+C
        tokio::signal::ctrl_c().await?;
        eprintln!("Shutting down");
    }

    Ok(())
}
