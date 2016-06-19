# go-speedtest

Go Command line interface for testing internet bandwidth using speedtest.net

## Usage

```
usage: speedtest [options]

Command line interface for testing internet bandwidth using speedtest.net.
--------------------------------------------------------------------------
https://github.com/sivel/go-speedtest

options:
  -csv
    Suppress verbose output, only show basic information in CSV format
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
