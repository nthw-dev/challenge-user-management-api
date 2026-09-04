## User Management API

### Objective (User Management API)

Build a RESTful API in Golang to manage users, using MongoDB for persistence, JWT for authentication, and clean code practices.

### Requirements (User Management API)

#### 1. User Model

Define a user entity with the following fields:

- `ID` (auto-generated)
- `Name` (string)
- `Email` (string, unique)
- `Password` (hashed)
- `CreatedAt` (timestamp)

#### 2. Authentication

Implement:

- User registration
- User authentication that returns a JWT token

JWT requirements:

- Protect endpoints with JWT
- Validate tokens via middleware
- Sign tokens using HMAC (`HS256`) with a secret key

#### 3. User Operations

Implement the following operations:

- Create a new user
- Fetch a user by ID
- List all users
- Update a user's name or email
- Delete a user

#### 4. MongoDB Integration

- Use the official Go MongoDB driver
- Persist and retrieve user data from MongoDB

#### 5. Middleware

- Implement logging middleware to capture HTTP method, path, and execution time

#### 6. Concurrency Task

- Run a background goroutine every 10 seconds to log the total number of users in the database

#### 7. Testing

- Write unit tests using Go's standard `testing` package
- Mock MongoDB interactions where appropriate

### Bonus (Optional, User Management API)

- **Containerization**: Add Docker and `docker-compose` support for the API and MongoDB
- **Abstraction**: Use Go interfaces to abstract MongoDB operations for better testability
- **Validation**: Implement input validation (for example, required fields and email format)
- **Graceful Shutdown**: Handle system signals using `context.Context`
- **gRPC Support**:
  - Define a `.proto` file for `CreateUser` and `GetUser`
  - Implement a gRPC server (optionally secure with token metadata)
- **Hexagonal Architecture**:
  - Structure the project using ports and adapters
  - Separate domain, application, and infrastructure layers
  - Decouple business logic from frameworks and drivers

### Deliverables (User Management API)

Provide a Git repository containing:

- `README.md` with setup and execution instructions
- A guide explaining how to generate and use JWT tokens
- Sample API requests and responses
- Documentation of assumptions or design decisions

### Evaluation Criteria (User Management API)

- Code quality, structure, and readability
- Correctness and completeness of the REST API
- Security and implementation of JWT
- Proper usage and abstraction of MongoDB
- Test coverage and effective mocking
- Idiomatic Go usage
- Bonus implementations (gRPC, Docker, validation, architecture)