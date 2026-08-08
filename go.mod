module github.com/TheGrimmChester/opm-api

go 1.22

require (
	github.com/TheGrimmChester/open-auth-go v0.0.0
	github.com/TheGrimmChester/open-cache-go v0.0.0
	github.com/TheGrimmChester/open-client-go v0.0.0-20260803093649-eb6d2f7a2423
	github.com/TheGrimmChester/open-http-go v0.0.0-20260804055231-a9462e336412
	github.com/TheGrimmChester/open-job-env-go v0.0.0
	github.com/TheGrimmChester/open-job-go v0.0.0-20260803091535-04d163946627
	github.com/TheGrimmChester/open-logger-go v0.2.0
	github.com/TheGrimmChester/open-tenant-go v0.3.0
	github.com/google/uuid v1.6.0
)

require (
	github.com/TheGrimmChester/open-crypto-go v0.0.0 // indirect
	github.com/TheGrimmChester/open-egress-proxy v0.0.0-20260808055639-6b52fa909452 // indirect
	github.com/golang-jwt/jwt/v5 v5.3.1 // indirect
)

replace github.com/TheGrimmChester/open-auth-go => ../Open-Auth-Go

replace github.com/TheGrimmChester/open-cache-go => ../Open-Cache-Go

replace github.com/TheGrimmChester/open-client-go => ../open-client-go

replace github.com/TheGrimmChester/open-crypto-go => ../Open-Crypto-Go

replace github.com/TheGrimmChester/open-job-env-go => ../Open-Job-Env-Go

replace github.com/TheGrimmChester/open-tenant-go => ../Open-Tenant-Go
