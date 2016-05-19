# connector

A lightweight Go server that we can use to query databases for integration.

Starts a HTTPS server on a given host and port e.g. https://127.0.0.1:8081 and responds to "tasks" to perform database queries and execute commands, responding with the result.

**Endpoints**

`/` : [GET] Health check. Responds with a success message if the server is online:

```json
{
    "type": "success",
    "body": "Digistorm Connector Online"
}
```

`/task` : [POST] Perform task. Connects to a database server using provided configuration and performs a query, returning a JSON encoded response.

Example request body:

```json
{
    "id": "573a6ec5cd45b",
    "type": "mssql.query",
    "config": {
        "type": "mssql",
        "dsn": "server=192.168.1.23;user id=sa;password=#SAPassword!;database=testing"
    },
    "payload": "SELECT * FROM dbo.users"
}
```

Example response:

```json
{
    "type": "success",
    "body": [
        {
            "email": "test@example.com",
            "id": "1"
        },
        {
            "email": "mark@example.com",
            "id": "2"
        },
        {
            "email": "another@example.com",
            "id": "3"
        }
    ]
}
```

**Supported Task Types**

"mysql.query"
"mysql.exec"
"mssql.query"
"mssql.exec"


## Installation


### Linux / OSX

#### Build From Source

```bash
go build connector.go
```

#### Run as Service

```bash
# Install the service:
sudo connector -service install

# Start the service:
connector -service start

# Stop the service:
connector -service stop

# Restart the service:
connector -service restart

# Uninstall the service:
sudo connector -service uninstall
```


### Windows

#### Build From Source (from Linux / OSX)

```bash
GOOS=windows GOARCH=386 go build -o connector.exe connector.go
```

#### Run as Service

Open a command prompt as Administrator.

```bash
# Install the service:
connector.exe -service install

# Start the service:
connector.exe -service start

# Stop the service:
connector.exe -service stop

# Restart the service:
connector.exe -service restart

# Uninstall the service:
connector.exe -service uninstall
```


## Usage

Ensure the service is running. Make a POST to the "/task" endpoint with a JSON payload e.g.

```bash
curl -X POST -d '{
    "id": "573a6ec5cd45b",
    "type": "mssql.query",
    "config": {
        "type": "mssql",
        "dsn": "server=192.168.1.23;user id=sa;password=#SAPassword!;database=testing"
    },
    "payload": "SELECT * FROM dbo.users"
}' "https://127.0.0.1:8081/task"
```
