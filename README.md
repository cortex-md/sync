# Cortex Sync

This repository contains the source code of cortex sync server, you can check the documentation of endpoints [here](/docs/api.md)

## How to self host the sync or contribute to the project

Cortex follow the idea of open source self hosting, so you can deploy the sync server yourself and configure it to work with your cortex instance, gaining total control over your data.

### Prerequisites

- You must have docker installed for a better experience, check the docs [here](https://docs.docker.com/engine/install/)
- If you are gonna contribute to the project, you must have go lang installed, check the doc [here](https://go.dev/doc/install)

### Installation

First, you need to clone this repository and navigate to the sync directory:

```bash
git clone https://github.com/cortex-md/sync.git
cd sync
```

Then, you can build the entire infra using docker compose:

```bash
docker compose up -d
```

This should start the sync server and make it available at `http://localhost:8080`, you can check via docker logs:

```bash
docker logs cortex-sync
```

### Enabling self hostage

To enable self hostage, you need to configure the app to use your local server to host the sync, you can read the [documentation](/docs/self-hosting.md) related to it.
