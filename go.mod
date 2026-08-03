module github.com/TheGrimmChester/opm-api

go 1.22

require (
	github.com/TheGrimmChester/open-auth-go v0.0.0
	github.com/TheGrimmChester/open-client-go v0.0.0
	github.com/TheGrimmChester/open-job-go v0.0.0
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/google/uuid v1.6.0
)

replace github.com/TheGrimmChester/open-auth-go => ../Open-Auth-Go

replace github.com/TheGrimmChester/open-client-go => ../Open-Client-Go

replace github.com/TheGrimmChester/open-job-go => ../Open-Job-Go
