// A lightweight HTTPS server to act as a connector between a local database and the Digistorm API.
package main

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	_ "github.com/denisenkom/go-mssqldb"
	_ "github.com/go-sql-driver/mysql"
	"github.com/kardianos/osext"
	"github.com/kardianos/service"
	"github.com/markokeeffe/mapquery"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"crypto/x509"
	"encoding/pem"
	"crypto/x509/pkix"
	"math/big"
	"time"
	"crypto/rand"
	"crypto/rsa"
	"crypto/ecdsa"
)

const (
	HOST                     = "127.0.0.1"
	PORT                     = "8081"
	AUTH_USER                = "digistormconnector"
	TASK_TYPE_DB_MYSQL_QUERY = "mysql.query"
	TASK_TYPE_DB_MYSQL_EXEC  = "mysql.exec"
	TASK_TYPE_DB_MSSQL_QUERY = "mssql.query"
	TASK_TYPE_DB_MSSQL_EXEC  = "mssql.exec"
)

var (
	svcLogger service.Logger  // Will write logs to the Windows event viewer
	svcFlag   string          // Service control flag e.g. "start" "stop" "uninstall"...
	config    ConnectorConfig // Config vars
)

/*
Wrapper for this executable
*/
type program struct {
	exit chan struct{}
}

/*
Configuration for this executable
*/
type ConnectorConfig struct {
	ApiKey string `json:"key"`
	Host   string `json:"host"`
	Port   string `json:"port"`
}

/**
Container for the executable program that can be run as a service
*/
type Program struct {
	Exit    chan struct{}
	Service service.Service
	Cmd     *exec.Cmd
}

/**
A task from the API to be executed locally, then a JSON response returned
*/
type Task struct {
	Id        string          `json:"id"`
	RawConfig json.RawMessage `json:"config"`
	Type      string          `json:"type"`
	Payload   string          `json:"payload"`
}

/**
Config for a DB task to initialise the DB connection
*/
type TaskDbConfig struct {
	Type string `json:"type"`
	Dsn  string `json:"dsn"`
}

/*
A wrapper for the information returned when executing an INSERT/DELETE query
*/
type DbExecResult struct {
	LastInsertId int64 `json:"last_insert_id"`
	RowsAffected int64 `json:"rows_affected"`
}

/**
Used to return responses to the task server e.g. `{"type": "error", "body": "Invalid API Key."}`
*/
type JsonResponse struct {
	Type string      `json:"type"`
	Body interface{} `json:"body"`
}

/*
Get the correct path to the installed executables config file
*/
func getAssetPath(name string) (string, error) {
	fullexecpath, err := osext.Executable()
	if err != nil {
		return "", err
	}

	dir, _ := filepath.Split(fullexecpath)

	return filepath.Join(dir, name), nil
}

/*
Read in configuration from a JSON config file
*/
func readConfigFile(configPath string) (connectorConfig ConnectorConfig, err error) {

	file, err := os.Open(configPath)
	defer file.Close()
	if err != nil {
		return connectorConfig, err
	}

	decoder := json.NewDecoder(file)
	err = decoder.Decode(&connectorConfig)
	if err != nil {
		return connectorConfig, err
	}

	return connectorConfig, nil
}

/*
Write configuration to a JSON config file
*/
func writeConfigFile(configPath string) error {

	configData, err := json.Marshal(config)
	if err != nil {
		return err
	}

	err = ioutil.WriteFile(configPath, configData, 0644)
	if err != nil {
		return err
	}

	return nil
}

/*
Load in command line arguments, and attempt to read configuration from a JSON config file.
If the file does not exist, it will be created.
If the user specifies any command line arguments, use them to override the values in the config file,
and write the changes to the file.
*/
func processConfig() error {

	apiKey := flag.String("key", "", "Digistorm API Key.")
	host := flag.String("host", HOST, "Host name for this server e.g. '184.33.65.12' or 'digistorm.myschool.qld.edu.au'")
	port := flag.String("port", PORT, "Port numer for tist server. Must be open to incoming requests at the firewall. e.g. 8081")
	flag.StringVar(&svcFlag, "service", "", "Control the system service.")

	flag.Parse()

	configPath, err := getAssetPath("conf.json")
	if err != nil {
		return err
	}

	configUpdate := false

	// Attempt to read config from a file, but do not return an error if it isn't there,
	// we can write to the file after processing the command line arguments
	config, err = readConfigFile(configPath)
	if err != nil {
		log.Println(err)
	}
	if config.ApiKey == "" || (config.ApiKey != *apiKey && *apiKey != "") {
		config.ApiKey = *apiKey
		configUpdate = true
	}
	if config.Host == "" || (config.Host != *host && *host != HOST) {
		config.Host = *host
		configUpdate = true
	}
	if config.Port == "" || (config.Port != *port && *port != PORT) {
		config.Port = *port
		configUpdate = true
	}

	if configUpdate == true {
		err = writeConfigFile(configPath)
		if err != nil {
			return err
		}
	}

	return nil
}

/*
Populate Task struct from the JSON request
*/
func parseTask(data []byte) (Task, error) {

	var task Task

	err := json.Unmarshal(data, &task)
	if err != nil {
		return task, err
	}

	fmt.Print("Task received: ")
	fmt.Println(task.Id)

	return task, err
}

/*
Get DB specific config to initialise a database connection
*/
func getTaskDbConfig(task Task) TaskDbConfig {
	var dbConfig TaskDbConfig
	err := json.Unmarshal(task.RawConfig, &dbConfig)
	errCheck(err)
	fmt.Print("Database Configuration: ")
	fmt.Println(dbConfig)

	return dbConfig
}

/*
Initialise database connection based on the task type
*/
func initDbConnection(task Task) *sql.DB {
	fmt.Println("Initilising Database Connection...")
	config := getTaskDbConfig(task)
	db, err := sql.Open(config.Type, config.Dsn)
	errCheck(err)

	return db
}

/*
Open a DB connection, execute a query and POST the result back to the API
*/
func processDbQuery(task Task) (interface{}, error) {

	fmt.Print("Querying database: ")
	fmt.Println(task.Payload)

	db := initDbConnection(task)
	db.SetMaxIdleConns(100)
	defer db.Close()

	rows, err := db.Query(task.Payload)
	if err != nil {
		return nil, err
	}

	mappedRows, err := mapquery.MapRows(rows)

	return mappedRows, err
}

/*
Open a DB connection, execute a query and POST the result back to the API
*/
func processDbExec(task Task) (DbExecResult, error) {

	fmt.Print("Executing statement: ")
	fmt.Println(task.Payload)

	db := initDbConnection(task)
	db.SetMaxIdleConns(100)
	defer db.Close()

	var response DbExecResult

	result, err := db.Exec(task.Payload)
	if err != nil {
		return response, err
	}
	lastInsertId, _ := result.LastInsertId()
	rowsAffected, _ := result.RowsAffected()

	response = DbExecResult{
		LastInsertId: lastInsertId,
		RowsAffected: rowsAffected,
	}

	return response, nil
}

/*
Parse HTTP request body for a task - should JSON decode the task and process it based on it's type
*/
func processTaskRequest(r *http.Request) (interface{}, error) {

	var response interface{}

	// Read the contents of the request body
	body, err := ioutil.ReadAll(io.LimitReader(r.Body, 1048576))
	if err != nil {
		return response, err
	}
	if err := r.Body.Close(); err != nil {
		return response, err
	}

	// Attempt to JSON decode the request body into a Task struct
	task, err := parseTask(body)
	if err != nil {
		return response, fmt.Errorf("Unable to parse JSON request body: %s", err)
	}

	switch task.Type {
	case TASK_TYPE_DB_MYSQL_QUERY, TASK_TYPE_DB_MSSQL_QUERY:
		response, err = processDbQuery(task)
		fmt.Println(response)
		if err != nil {
			err = fmt.Errorf("Database error: %s", err)
		}
	case TASK_TYPE_DB_MYSQL_EXEC, TASK_TYPE_DB_MSSQL_EXEC:
		response, err = processDbExec(task)
		if err != nil {
			err = fmt.Errorf("Database error: %s", err)
		}
	default:
		return response, fmt.Errorf("Unknown task type: %s", task.Type)
	}

	return response, err
}

/*
Get the HTTP basic auth headers and check against the configured username and API key
*/
func checkAuth(w http.ResponseWriter, r *http.Request) bool {
	s := strings.SplitN(r.Header.Get("Authorization"), " ", 2)
	if len(s) != 2 {
		return false
	}

	b, err := base64.StdEncoding.DecodeString(s[1])
	if err != nil {
		return false
	}

	pair := strings.SplitN(string(b), ":", 2)
	if len(pair) != 2 {
		return false
	}

	return pair[0] == AUTH_USER && pair[1] == config.ApiKey
}

/*
Wrapper function to handle HTTP requests, checking HTTP basic authorisation credentials
*/
func handleAuthMiddleware(w http.ResponseWriter, r *http.Request, handler func(http.ResponseWriter, *http.Request)) {
	if checkAuth(w, r) {
		handler(w, r)
		return
	}

	w.Header().Set("WWW-Authenticate", `Basic realm="MY REALM"`)
	w.WriteHeader(401)
	w.Write([]byte("401 Unauthorized\n"))
}

/*
Handle an HTTP request to the / URL - display a success message
*/
func handleRoot(w http.ResponseWriter, r *http.Request) {
	writeResponse(w, http.StatusOK, JsonResponse{
		Type: "success",
		Body: "Digistorm Connector Online",
	})
}

/*
Handle an HTTP request to the /task URL - should contain a JSON encoded task in the request body
*/
func handleTask(w http.ResponseWriter, r *http.Request) {

	rawResponse, err := processTaskRequest(r)

	if err != nil {
		writeResponse(w, http.StatusInternalServerError, JsonResponse{
			Type: "error",
			Body: fmt.Sprintf("%s", err),
		})
		return
	}

	writeResponse(w, http.StatusOK, JsonResponse{
		Type: "success",
		Body: rawResponse,
	})

}

func writeResponse (w http.ResponseWriter, status int, response JsonResponse) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.WriteHeader(status)
	err := json.NewEncoder(w).Encode(response)
	errCheck(err)
}

func publicKey(priv interface{}) interface{} {
	switch k := priv.(type) {
	case *rsa.PrivateKey:
		return &k.PublicKey
	case *ecdsa.PrivateKey:
		return &k.PublicKey
	default:
		return nil
	}
}
func pemBlockForKey(priv interface{}) *pem.Block {
	switch k := priv.(type) {
	case *rsa.PrivateKey:
		return &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}
	case *ecdsa.PrivateKey:
		b, err := x509.MarshalECPrivateKey(k)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Unable to marshal ECDSA private key: %v", err)
			os.Exit(2)
		}
		return &pem.Block{Type: "EC PRIVATE KEY", Bytes: b}
	default:
		return nil
	}
}

/*
Start listening on the configured address
*/
func startServer() {
	serverAddress := fmt.Sprintf("%s:%s", config.Host, config.Port)

	caCertPath, err := getAssetPath("certs/ca/ca.crt")
	errCheckFatal(err)
	certPath, err := getAssetPath("certs/server/server.crt")
	errCheckFatal(err)
	keyPath, err := getAssetPath("certs/server/server.key")
	errCheckFatal(err)

	// Load CA cert
	caCert, err := ioutil.ReadFile(caCertPath)
	if err != nil {
		log.Fatal(err)
	}
	//
	//cert, err := ioutil.ReadFile(certPath)
	//if err != nil {
	//	log.Fatal(err)
	//}

	var block *pem.Block
	block, _ = pem.Decode(caCert)

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(string(cert.Signature))

	//fmt.Println(cert)

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		log.Fatal(err)
	}

	notBefore := time.Now()
	notAfter := notBefore.Add(365 * 24 * time.Hour)

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		log.Printf("failed to generate serial number: %s", err)
		log.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Digistorm"},
		},
		NotBefore: notBefore,
		NotAfter:  notAfter,

		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:		[]string{serverAddress},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, cert, publicKey(priv), priv)
	if err != nil {
		log.Printf("Failed to create certificate: %s", err)
		log.Fatal(err)
	}

	certOut, err := os.Create(certPath)
	if err != nil {
		log.Printf("failed to open " + certPath + " for writing: %s", err)
		log.Fatal(err)
	}
	pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	certOut.Close()
	log.Print("written cert.pem\n")

	keyOut, err := os.OpenFile(keyPath, os.O_WRONLY | os.O_CREATE | os.O_TRUNC, 0600)
	if err != nil {
		log.Print("failed to open " + keyPath + " for writing:", err)
		log.Fatal(err)
	}
	pem.Encode(keyOut, pemBlockForKey(priv))
	keyOut.Close()
	log.Print("written key.pem\n")

	//block, _ := pem.Decode(cert)
	//if block == nil {
	//	panic("failed to parse certificate PEM")
	//}
	//parsedCert, err := x509.ParseCertificate(cert)
	//if err != nil {
	//	panic("failed to parse certificate: " + err.Error())
	//}
	//
	//fmt.Println(parsedCert)





	//// Check if the cert files are available.
	//err = httpscerts.Check(certPath, keyPath)
	//// If they are not available, generate new ones.
	//if err != nil {
	//	err = httpscerts.Generate(certPath, keyPath, serverAddress)
	//	if err != nil {
	//		log.Fatal("Error: Couldn't create https certs.")
	//	}
	//}
	//
	//http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
	//	handleAuthMiddleware(w, r, handleRoot)
	//})
	//http.HandleFunc("/task", func(w http.ResponseWriter, r *http.Request) {
	//	handleAuthMiddleware(w, r, handleTask)
	//})
	//fmt.Println(fmt.Sprintf("Starting server on address: %s", serverAddress))
	//http.ListenAndServeTLS(serverAddress, certPath, keyPath, nil)
}

func (p *program) Start(s service.Service) error {
	if service.Interactive() {
		svcLogger.Info("Connector running in terminal.")
	} else {
		svcLogger.Info("Connector running under service manager.")
	}
	p.exit = make(chan struct{})

	// Start should not block. Do the actual work async.
	go p.run()
	return nil
}
func (p *program) run() error {
	svcLogger.Infof("Connector running on platform: %v.", service.Platform())
	svcLogger.Infof("Config: %v", config)

	// By this point, there should be an API key in the config - show the user an error if it hasn't been provided
	if len(config.ApiKey) == 0 {
		errCheckFatal(errors.New("API key must be specified e.g. 'connector.exe -key=ABC123'"))
	}

	startServer()

	return nil
}
func (p *program) Stop(s service.Service) error {
	// Any work in Stop should be quick, usually a few seconds at most.
	svcLogger.Info("Connector stopping")
	close(p.exit)
	return nil
}

func errCheck(err error) {
	if err != nil {
		svcLogger.Error(err)
	}
}
func errCheckFatal(err error) {
	if err != nil {
		svcLogger.Error(err)
		log.Fatal(err)
	}
}

// Service setup.
//   Define service config.
//   Create the service.
//   Setup the logger.
//   Handle service controls (optional).
//   Run the service.
func main() {

	svcConfig := &service.Config{
		Name:        "DigistormConnector",
		DisplayName: "Digistorm Connector",
		Description: "A lightweight HTTPS server to act as a connector between a local database and the Digistorm API.",
	}

	prg := &program{}
	s, err := service.New(prg, svcConfig)
	if err != nil {
		log.Fatal(err)
	}
	errs := make(chan error, 5)
	svcLogger, err = s.Logger(errs)
	if err != nil {
		log.Fatal(err)
	}

	go func() {
		for {
			err := <-errs
			if err != nil {
				log.Print(err)
			}
		}
	}()

	err = processConfig()
	errCheckFatal(err)

	if len(svcFlag) != 0 {
		err := service.Control(s, svcFlag)
		if err != nil {
			log.Printf("Valid actions: %q\n", service.ControlAction)
			log.Fatal(err)
		}
		return
	}

	err = s.Run()
	errCheckFatal(err)
}
