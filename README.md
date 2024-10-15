# Shorty

Shorty is a simple and efficient URL shortener written in Go. It provides an easy way to create short, memorable links for long URLs.

![Shorty Logo](https://github-production-user-asset-6210df.s3.amazonaws.com/96031819/260317630-6dc584a5-eaa5-442d-8afe-1f04238caab8.png)

## Features

- Create short URLs for long links
- Redirect short URLs to their original long URLs
- View statistics for link usage
- Simple web interface
- SQLite database for storing URL mappings
- Configurable via JSON file

## Installation

1. Clone the repository:
   ```
   git clone https://github.com/donuts-are-good/shorty.git
   ```

2. Navigate to the project directory:
   ```
   cd shorty
   ```

3. Build the project:
   ```
   go build
   ```

## Configuration

Shorty uses a JSON configuration file named `shorty.config`. You can modify this file to change the database name, server port, routes, and short URL settings.

Example configuration:

```json
{
  "database": {
  "name": "./url_mapping.db"
  },
  "server": {
  "port": ":9130"
  },
  "routes": {
    "index": "/",
    "create": "/create",
    "redirect": "/r/"
  },
  "shortURL": {
    "length": 8,
    "charset": "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
  }
}
```
## Usage

To run Shorty:

```
./shorty
```

The server will start on the port specified in the configuration file (default is 9130).

## Running with appserve

[appserve](https://github.com/donuts-are-good/appserve) is a reverse proxy server with automatic HTTPS. To run Shorty with appserve:

1. Install appserve:
   ```
   git clone https://github.com/donuts-are-good/appserve.git
   cd appserve
   go build
   ```

2. Start appserve:
   ```
   ./appserve
   ```

3. In the appserve interactive shell, add a route for Shorty:
   ```
   add yourdomain.com 9130
   ```
   Replace `yourdomain.com` with your actual domain and `9130` with the port Shorty is running on.

4. Start Shorty as described in the Usage section.

Now, appserve will handle HTTPS and route requests to Shorty.

## License

Shorty is released under the MIT License. See the [LICENSE](LICENSE) file for details.

## Live Instance

A running instance of Shorty is available at [https://goby.lol](https://goby.lol).
