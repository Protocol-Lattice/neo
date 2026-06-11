module github.com/Protocol-Lattice/neo/examples

go 1.26.3

require (
	github.com/Protocol-Lattice/neo v0.0.0
	github.com/joho/godotenv v1.5.1
	github.com/shopspring/decimal v1.4.0
	github.com/steebchen/prisma-client-go v0.47.0
	golang.org/x/crypto v0.32.0
	google.golang.org/grpc v1.71.3
)

require go.mongodb.org/mongo-driver/v2 v2.0.1 // indirect

replace github.com/Protocol-Lattice/neo => ..
