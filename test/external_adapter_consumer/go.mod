module github.com/osmai4603-collab/accounting_engine/test/external_adapter_consumer

go 1.26.0

require (
	github.com/osmai4603-collab/accounting_engine v0.0.0
	github.com/shopspring/decimal v1.6.0
)

require (
	github.com/aws/aws-sdk-go-v2 v1.47.8 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.26 // indirect
	github.com/aws/aws-sdk-go-v2/config v1.33.17 // indirect
	github.com/aws/aws-sdk-go-v2/credentials v1.20.17 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.20.8 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.5.11 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.8.11 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.5.11 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.24 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.11.12 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.14.11 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.20.11 // indirect
	github.com/aws/aws-sdk-go-v2/service/s3 v1.114.9 // indirect
	github.com/aws/aws-sdk-go-v2/service/signin v1.13.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.39.4 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.44.4 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.51.9 // indirect
	github.com/aws/smithy-go v1.28.8 // indirect
	github.com/google/uuid v1.6.0 // indirect
	golang.org/x/text v0.43.0 // indirect
)

// LOCAL PRE-RELEASE VERIFICATION ONLY.
//
// This replace lets the independent module build against the working tree
// before a version is published. It is not part of the published-consumer
// contract. `scripts/check_external_adapter_consumer.sh --published` copies the
// module, removes this directive and builds it from a file-based module proxy,
// which proves the same project consumes a versioned module without a replace.
replace github.com/osmai4603-collab/accounting_engine => ../..
