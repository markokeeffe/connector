package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	_ "github.com/denisenkom/go-mssqldb"
	_ "github.com/go-sql-driver/mysql"
	"github.com/kardianos/service"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os/exec"
	"github.com/markokeeffe/mapquery"
	"github.com/kabukky/httpscerts"
	"os"
	"path"
	"runtime"
	"strings"
	"encoding/base64"
)

const (
	HOST = "127.0.0.1"
	PORT = "8081"
	AUTH_USER = "digistormconnector"
	TASK_TYPE_DB_MYSQL_QUERY = "mysql.query"
	TASK_TYPE_DB_MYSQL_EXEC  = "mysql.exec"
	TASK_TYPE_DB_MSSQL_QUERY = "mssql.query"
	TASK_TYPE_DB_MSSQL_EXEC  = "mssql.exec"
)

var (
	svcFlag	string // Service control flag e.g. "start" "stop" "uninstall"...
	svcLogger service.Logger // logger for the service
	config    Config
)

type Config struct {
	ApiKey	string `json:"key"`
	Host	string `json:"host"`
	Port	string `json:"port"`
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

/**
Read in configuration from a JSON config file - this can be overridden by command line arguments.
If any config is overridden, the `config.json` file is updated.
*/
func loadConfiguration() error {
	apiKey := flag.String("key", "", "Digistorm API Key.")
	host := flag.String("host", HOST, "Host name for this server e.g. '184.33.65.12' or 'digistorm.myschool.qld.edu.au'")
	port := flag.String("port", PORT, "Port number for tist server. Must be open to incoming requests at the firewall. e.g. 8081")
	flag.StringVar(&svcFlag, "service", "", "Control the system service.")

	flag.Parse()

	_, filename, _, _ := runtime.Caller(1)
	configFilePath := path.Join(path.Dir(filename), "conf.json")
	fmt.Println(configFilePath)

	file, err := os.Open(configFilePath)
	if err != nil {
		panic(err)
	}
	decoder := json.NewDecoder(file)
	err = decoder.Decode(&config)
	if err != nil {
		panic(err)
	}

	var configChanged bool = false

	if config.ApiKey == "" {
		config.ApiKey = *apiKey
		configChanged = true
	}
	if config.Host == "" {
		config.Host = *host
		configChanged = true
	}
	if config.Port == "" {
		config.Port = *port
		configChanged = true
	}

	if configChanged == true {
		configData, err := json.Marshal(config)
		if err != nil {
			panic(err)
		}
		err = ioutil.WriteFile(configFilePath, configData, 0644)
		if err != nil {
			panic(err)
		}
	}

	return nil
}

/**
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

/**
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

/**
Initialise database connection based on the task type
*/
func initDbConnection(task Task) *sql.DB {
	fmt.Println("Initilising Database Connection...")
	config := getTaskDbConfig(task)
	db, err := sql.Open(config.Type, config.Dsn)
	errCheck(err)

	return db
}

/**
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

/**
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

/**
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

func checkAuth(w http.ResponseWriter, r *http.Request) bool {
	s := strings.SplitN(r.Header.Get("Authorization"), " ", 2)
	if len(s) != 2 { return false }

	b, err := base64.StdEncoding.DecodeString(s[1])
	if err != nil { return false }

	pair := strings.SplitN(string(b), ":", 2)
	if len(pair) != 2 { return false }

	return pair[0] == AUTH_USER && pair[1] == config.ApiKey
}

func handleAuthMiddleware (w http.ResponseWriter, r *http.Request, handler func(http.ResponseWriter, *http.Request)) {
	if checkAuth(w, r) {
		handler(w, r)
		return
	}

	w.Header().Set("WWW-Authenticate", `Basic realm="MY REALM"`)
	w.WriteHeader(401)
	w.Write([]byte("401 Unauthorized\n"))
}

/**
Handle an HTTP request to the / URL - display a success message
 */
func handleRoot(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	response := JsonResponse{
		Type: "success",
		Body: "Digistorm Connector Online",
	}
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		panic(err)
	}
}

/**
Handle an HTTP request to the /task URL - should contain a JSON encoded task in the request body
 */
func handleTask(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Content-Type", "application/json; charset=UTF-8")

	rawResponse, err := processTaskRequest(r)

	if err != nil {
		response := JsonResponse{
			Type: "error",
			Body: fmt.Sprintf("%s", err),
		}
		w.WriteHeader(http.StatusInternalServerError)
		if err := json.NewEncoder(w).Encode(response); err != nil {
			panic(err)
		}
	} else {
		response := JsonResponse{
			Type: "success",
			Body: rawResponse,
		}
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(response); err != nil {
			panic(err)
		}
	}

}

/**
Handle an error - returns true if error was handled
*/
func errCheck(err error) bool {
	if err != nil {
		fmt.Println(err)

		return true
	}

	return false
}

func (p *Program) Start(s service.Service) error {
	svcLogger.Info("Starting...")
	// Start should not block. Do the actual work async.
	go p.run()
	return nil
}
func (p *Program) run() {
	svcLogger.Info("Running...")

	serverAddress := fmt.Sprintf("%s:%s", config.Host, config.Port)

	// Check if the cert files are available.
	err := httpscerts.Check("server.cert.pem", "server.key.pem")
	// If they are not available, generate new ones.
	if err != nil {
		err = httpscerts.Generate("server.cert.pem", "server.key.pem", serverAddress)
		if err != nil {
			log.Fatal("Error: Couldn't create https certs.")
		}
	}

	http.HandleFunc("/", func (w http.ResponseWriter, r *http.Request) {
		handleAuthMiddleware(w, r, handleRoot)
	})
	http.HandleFunc("/task", func (w http.ResponseWriter, r *http.Request) {
		handleAuthMiddleware(w, r, handleTask)
	})
	fmt.Println(fmt.Sprintf("Starting server on address: %s", serverAddress))
	http.ListenAndServeTLS(serverAddress, "server.cert.pem", "server.key.pem", nil)
}
func (p *Program) Stop(s service.Service) error {
	svcLogger.Info("Stopping...")
	// Stop should not block. Return with a few seconds.
	return nil
}

func main() {

	svcConfig := &service.Config{
		Name:        "DigistormConnector",
		DisplayName: "Digistorm Connector",
		Description: "A lightweight server to process queries from Digistorm and return results over HTTP.",
	}

	program := &Program{}

	s, err := service.New(program, svcConfig)
	if err != nil {
		panic(err)
	}

	svcLogger, err = s.Logger(nil)
	if err != nil {
		panic(err)
	}

	err = loadConfiguration()
	if err != nil {
		panic(err)
	}

	if len(config.ApiKey) == 0 {
		log.Fatal("API key must be specified e.g. 'connector.exe -key=ABC123'")
	}

	if len(svcFlag) != 0 {

		err := service.Control(s, svcFlag)
		if err != nil {
			log.Printf("Valid actions: %q\n", service.ControlAction)
			log.Fatal(err)
		}
		return
	}

	err = s.Run()
	panic(err)
}
