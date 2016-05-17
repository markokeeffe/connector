# connector
A lightweight Go server that we can use to query databases for integration.


## Usage

### Linux / OSX

#### Build From Source

```bash
    go build connector.go
```

#### Run as Service

Install the service:
```bash
    sudo connector -service install
```

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
