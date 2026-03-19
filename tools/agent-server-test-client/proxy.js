// Zero-dependency reverse proxy for agent-server test client.
// Serves index.html and proxies HTTP + WebSocket to the upstream agent-server.

const http = require("http");
const net = require("net");
const fs = require("fs");
const path = require("path");
const url = require("url");

const UPSTREAM = process.env.AGENT_SERVER_UPSTREAM || "http://localhost:8080";
const PORT = parseInt(process.env.PROXY_PORT || "3000", 10);
const INDEX_PATH = path.join(__dirname, "index.html");

const upstream = new URL(UPSTREAM);
const UPSTREAM_HOST = upstream.hostname;
const UPSTREAM_PORT = upstream.port
  ? parseInt(upstream.port, 10)
  : (upstream.protocol === "https:" ? 443 : 80);

// --- Static file serving ---

function serveIndex(res) {
  fs.readFile(INDEX_PATH, (err, data) => {
    if (err) {
      res.writeHead(500, { "Content-Type": "text/plain" });
      res.end("Failed to read index.html");
      return;
    }
    res.writeHead(200, {
      "Content-Type": "text/html; charset=utf-8",
      "Content-Length": data.length,
      "Cache-Control": "no-cache",
    });
    res.end(data);
  });
}

// --- HTTP reverse proxy ---

function proxyRequest(clientReq, clientRes) {
  const options = {
    hostname: UPSTREAM_HOST,
    port: UPSTREAM_PORT,
    path: clientReq.url,
    method: clientReq.method,
    headers: { ...clientReq.headers, host: `${UPSTREAM_HOST}:${UPSTREAM_PORT}` },
  };

  const proxyReq = http.request(options, (proxyRes) => {
    clientRes.writeHead(proxyRes.statusCode, proxyRes.headers);
    proxyRes.pipe(clientRes, { end: true });
  });

  proxyReq.on("error", (err) => {
    console.error(`Proxy error: ${err.message}`);
    if (!clientRes.headersSent) {
      clientRes.writeHead(502, { "Content-Type": "application/json" });
      clientRes.end(JSON.stringify({ error: "upstream unreachable" }));
    }
  });

  clientReq.pipe(proxyReq, { end: true });
}

// --- WebSocket tunnel (raw TCP) ---

function tunnelWebSocket(clientReq, clientSocket, head) {
  const upstreamSocket = net.connect(UPSTREAM_PORT, UPSTREAM_HOST, () => {
    // Rewrite headers for Origin check: nhooyr.io/websocket verifies Origin matches Host.
    const headers = { ...clientReq.headers };
    headers.host = `${UPSTREAM_HOST}:${UPSTREAM_PORT}`;
    headers.origin = `http://${UPSTREAM_HOST}:${UPSTREAM_PORT}`;

    // Build raw HTTP upgrade request
    let reqLine = `${clientReq.method} ${clientReq.url} HTTP/1.1\r\n`;
    for (const [key, value] of Object.entries(headers)) {
      if (Array.isArray(value)) {
        for (const v of value) reqLine += `${key}: ${v}\r\n`;
      } else {
        reqLine += `${key}: ${value}\r\n`;
      }
    }
    reqLine += "\r\n";

    upstreamSocket.write(reqLine);
    if (head && head.length > 0) {
      upstreamSocket.write(head);
    }

    // Pipe bidirectionally
    upstreamSocket.pipe(clientSocket);
    clientSocket.pipe(upstreamSocket);
  });

  upstreamSocket.on("error", (err) => {
    console.error(`WebSocket tunnel error: ${err.message}`);
    clientSocket.end();
  });

  clientSocket.on("error", (err) => {
    console.error(`Client socket error: ${err.message}`);
    upstreamSocket.end();
  });
}

// --- Server setup ---

const server = http.createServer((req, res) => {
  const parsed = url.parse(req.url);
  if (parsed.pathname === "/" || parsed.pathname === "/index.html") {
    serveIndex(res);
  } else {
    proxyRequest(req, res);
  }
});

server.on("upgrade", (req, socket, head) => {
  tunnelWebSocket(req, socket, head);
});

server.listen(PORT, () => {
  console.log(`Proxy listening on http://localhost:${PORT}`);
  console.log(`Upstream: ${UPSTREAM}`);
});
