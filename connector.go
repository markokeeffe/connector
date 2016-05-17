package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	_ "github.com/denisenkom/go-mssqldb"
	_ "github.com/go-sql-driver/mysql"
	"github.com/kardianos/service"
	"github.com/kabukky/httpscerts"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os/exec"
)

const (
	HOST = "connector.test"
	PORT = "8081"
	TASK_TYPE_DB_MYSQL_QUERY = "mysql.query"
	TASK_TYPE_DB_MYSQL_EXEC  = "mysql.exec"
	TASK_TYPE_DB_MSSQL_QUERY = "mssql.query"
	TASK_TYPE_DB_MSSQL_EXEC  = "mssql.exec"
)

var (
	svcLogger service.Logger // logger for the service
)

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
type DBTaskConfig struct {
	Type string `json:"type"`
	Dsn  string `json:"dsn"`
}

type DBExecResult struct {
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
Used to map rows with unknown columns from a DB query so we can add them to a JSON response
*/
type MapStringScan struct {
	// cp are the column pointers
	cp []interface{}
	// row contains the final result
	row      map[string]string
	colCount int
	colNames []string
}

/**
Initialise a mop for a row in the DB query result that will be updated with `rows.Scan()`
*/
func newMapStringScan(columnNames []string) *MapStringScan {
	lenCN := len(columnNames)
	s := &MapStringScan{
		cp:       make([]interface{}, lenCN),
		row:      make(map[string]string, lenCN),
		colCount: lenCN,
		colNames: columnNames,
	}
	for i := 0; i < lenCN; i++ {
		s.cp[i] = new(sql.RawBytes)
	}
	return s
}

/**
Update a row map from the db query result
*/
func (s *MapStringScan) Update(rows *sql.Rows) error {
	if err := rows.Scan(s.cp...); err != nil {
		return err
	}

	for i := 0; i < s.colCount; i++ {
		if rb, ok := s.cp[i].(*sql.RawBytes); ok {
			s.row[s.colNames[i]] = string(*rb)
			*rb = nil // reset pointer to discard current value to avoid a bug
		} else {
			return fmt.Errorf("Cannot convert index %d column %s to type *sql.RawBytes", i, s.colNames[i])
		}
	}
	return nil
}

/**
Get a map representing a row from DB query results
*/
func (s *MapStringScan) Get() map[string]string {
	rowCopy := make(map[string]string, len(s.row))
	for k, v := range s.row {
		rowCopy[k] = v
	}
	return rowCopy
}

/**
Fetch a pending task from the API and populate a Task from the JSON response
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
func getDbTaskConfig(task Task) DBTaskConfig {
	var dbConfig DBTaskConfig
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
	config := getDbTaskConfig(task)
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

	columnNames, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	var response []interface{}

	rc := newMapStringScan(columnNames)
	for rows.Next() {
		err := rc.Update(rows)
		if err != nil {
			fmt.Println(err)
		}

		response = append(response, rc.Get())
	}
	rows.Close()

	return response, nil
}

/**
Open a DB connection, execute a query and POST the result back to the API
*/
func processDbExec(task Task) (DBExecResult, error) {

	fmt.Print("Executing statement: ")
	fmt.Println(task.Payload)

	db := initDbConnection(task)
	db.SetMaxIdleConns(100)
	defer db.Close()

	var response DBExecResult

	result, err := db.Exec(task.Payload)
	if err != nil {
		return response, err
	}
	lastInsertId, _ := result.LastInsertId()
	rowsAffected, _ := result.RowsAffected()

	response = DBExecResult{
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

/**
Container for the executable program that can be run as a service
*/
type Program struct {
	Exit    chan struct{}
	Service service.Service
	Cmd     *exec.Cmd
}

func (p *Program) Start(s service.Service) error {
	svcLogger.Info("Starting...")
	// Start should not block. Do the actual work async.
	go p.run()
	return nil
}
func (p *Program) run() {
	svcLogger.Info("Running...")

	// Check if the cert files are available.
	err := httpscerts.Check("cert.pem", "key.pem")
	// If they are not available, generate new ones.
	if err != nil {
		err = httpscerts.Generate("cert.pem", "key.pem", fmt.Sprintf("%s:%s", HOST, PORT))
		if err != nil {
			log.Fatal("Error: Couldn't create https certs.")
		}

		// TODO POST the certs to Digistorm API to be stored against the API user of this exe

	}

	http.HandleFunc("/", handleRoot)
	http.HandleFunc("/task", handleTask)
	fmt.Println("Starting server...")
	http.ListenAndServeTLS(fmt.Sprintf("%s:%s", HOST, PORT), "cert.pem", "key.pem", nil)
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

	svcFlag := flag.String("service", "", "Control the system service.")
	flag.Parse()

	if len(*svcFlag) != 0 {

		err := service.Control(s, *svcFlag)
		if err != nil {
			log.Printf("Valid actions: %q\n", service.ControlAction)
			log.Fatal(err)
		}
		return
	}

	err = s.Run()
	panic(err)
}
