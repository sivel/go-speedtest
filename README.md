# speedtest

Command line interface for testing internet bandwidth using speedtest.net written in Go.

This application utilizes the pure socket communication in current use by speedtest.net instead of the older HTTP based tests.

This project is still in development and should be considered experimental, see https://github.com/sivel/speedtest-cli for a stable command line client.

## Download

Downloads are available from the [releases page](https://github.com/sivel/speedtest/releases).

## Usage

```
usage: speedtest [options]

Command line interface for testing internet bandwidth using speedtest.net.
--------------------------------------------------------------------------
https://github.com/sivel/speedtest

options:
  -csv
    Suppress verbose output, only show basic information in CSV format
  -influx
    Suppress verbose output, only show basic information in InfluxDB line protocol format
  -json
    Suppress verbose output, only show basic information in JSON format
  -list
    Display a list of speedtest.net servers sorted by distance
  -server int
    Specify a server ID to test against
  -share
    Generate and provide a URL to the speedtest.net share results image
  -simple
    Suppress verbose output, only show basic information
  -source string
    Source IP address to bind to
  -timeout int
    Timeout in seconds (default 10)
  -version
    Show the version number and exit
  -xml
    Suppress verbose output, only show basic information in XML format
```

## Troubleshooting

#### Port Restrictions

This application will typically communicate via tcp/443 (HTTPS) and tcp/8080.

tcp/443 is be used for obtaining the speedtest.net configuration and server lists.

tcp/8080 is used for socket communication with the speedtest.net test servers. This is a custom protocol and not HTTP based.
