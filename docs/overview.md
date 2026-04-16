# Ascaris Overview

## Primary Purpose

Ascaris is a versatile coding harness built primarily in Go, designed to streamline and automate the software development workflow. It serves as a comprehensive development environment and task execution framework that can be used for programming tasks across multiple languages and environments.

## Key Characteristics

### 1. Language Agnostic Framework
While built in Go, Ascaris is designed as a general-purpose harness that can be used for programming tasks across multiple languages, not limited to Go development.

### 2. Core Functionalities

- **Task Automation**: Automates routine development tasks like building, testing, and deploying applications
- **Parallel Execution**: Agent-based architecture enables concurrent task processing for improved efficiency
- **CLI Interface**: Provides both interactive terminal chat and command-line interface for flexible task management
- **Session Management**: Tracks and manages development sessions for continuity and reproducibility
- **Worker System**: Manages multiple workers for distributed task execution across complex workflows

### 3. Advanced Features

- **Security Review**: Built-in security auditing and vulnerability scanning capabilities
- **Bug Finding**: Includes comprehensive bug detection tools:
  - Fuzz testing for discovering edge cases
  - Crash triage for analyzing failures
  - Bug hunting for logic and memory issues
  - Code review automation
- **OAuth Integration**: Authentication and authorization support for secure operations
- **Plugin System**: Extensible architecture allowing custom functionality additions
- **MCP Support**: Model Context Protocol integration for AI-powered development assistance
- **Recovery Mechanisms**: Automatic error handling and recovery strategies to maintain workflow stability

### 4. Development Tools

- **Team Management**: Collaborative work support for distributed teams
- **Cron Scheduling**: Recurring task automation
- **State Management**: Tracks execution state across sessions
- **Configuration Management**: Flexible configuration system supporting multiple sources
- **Prompt-Based Interactions**: AI model integration for intelligent development assistance

## Architecture

### Modular Design
The repository is structured with a highly modular internal architecture:

- **`cmd/ascaris`**: Main executable entrypoint
- **`internal/`**: Core packages organized by functionality:
  - `agents/`: Parallel task execution agents
  - `api/`: API provider integrations
  - `cli/`: Command-line interface implementation
  - `runtime/`: Task execution runtime
  - `sessions/`: Session state management
  - `tools/`: Built-in development tools
  - `plugins/`: Plugin system infrastructure
  - `oauth/`: Authentication and authorization
  - `recovery/`: Error recovery mechanisms
  - `state/`: Worker and task state tracking
  - `securityreview/`: Security scanning capabilities

### Execution Model
Ascaris uses a worker-based execution model where:
1. Tasks are dispatched to workers
2. Workers execute tasks in parallel when possible
3. State is tracked throughout execution
4. Recovery mechanisms handle failures automatically
5. Results are aggregated and reported back to the user

### Configuration System
Multi-layered configuration approach:
- User-level settings
- Project-level settings
- Local overrides
- Environment variable support

This architecture makes Ascaris a comprehensive solution for modern development workflows, providing infrastructure for automating, managing, and optimizing software development tasks across various programming languages and environments.
