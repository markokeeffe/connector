# connector
A lightweight Go server that we can use to query databases for integration.


## Installation

### Linux / OSX

#### Build From Source

```bash
    go build connector.go
```

#### Run as Service

Install the service: `sudo connector -service install`

Start the service:
```bash
    sudo connector -service start
```

Stop the service:
```bash
    sudo connector -service stop
```

Restart the service:
```bash
    sudo connector -service restart
```

Uninstall the service:
```bash
    sudo connector -service uninstall
```


### Windows

#### Build From Source

```bash
    GOOS=windows GOARCH=386 go build -o connector.exe connector.go
```

#### Run as Service

Install the service:
```bash
    sudo connector.exe -service install
```

Start the service:
```bash
    sudo connector.exe -service start
```

Stop the service:
```bash
    sudo connector.exe -service stop
```

Restart the service:
```bash
    sudo connector.exe -service restart
```

Uninstall the service:
```bash
    sudo connector.exe -service uninstall
```


## Usage

Ensure the service is running. Make a POST to the "/task" endpoint with a JSON payload e.g.

```bash
    curl -X POST -H "Cache-Control: no-cache" -d '{
    	"id": "573a6ec5cd45b",
    	"type": "mssql.query",
    	"config": {
    		"type": "mssql",
    		"dsn": "server=192.168.1.23;user id=sa;password=#SAPassword!;database=testing"
    	},
    	"payload": "SELECT * FROM dbo.users"
    }' "https://127.0.0.1:8081/task"
```