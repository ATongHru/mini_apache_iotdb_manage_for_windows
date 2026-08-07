module mini-apache-iotdb-manager

go 1.24.0

require (
	github.com/apache/iotdb-client-go v1.3.7
	github.com/apache/thrift v0.15.0
)

replace github.com/apache/iotdb-client-go => ./third_party/iotdb-client-go
replace github.com/apache/thrift => ./third_party/thrift
