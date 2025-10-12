
# 🧠 MiniShop Observability Lab

A minimal e-commerce backend written in Go, designed **not** to sell products but to practice **observability engineering** — logs, metrics, and distributed tracing.
You’ll build a small system with full visibility into its behavior using Prometheus, Grafana, Loki, Jaeger, and OpenTelemetry.

---

## 🎯 Objective

Build a mini e-commerce backend with three simple APIs:

* `POST /order` — Create an order (returns `order_id`)
* `POST /payment/pay` — Simulate payment (random success/failure)

The goal isn’t business logic — it’s **observability completeness**.

---

## 🧩 Functional Requirements

| API                      | Function         | Description                                       |
| ------------------------ | ---------------- | ------------------------------------------------- |
| `POST /order`            | Create Order     | Simulate creating an order and return an order ID |
| `POST /payment/pay`      | Payment          | Simulate payment success or failure (randomized)  |

---

## 🪄 Observability Requirements

### **1. Logs**

* Log every API request with structured fields.
* Log failure reasons with context.
* Logs should be visible in **Grafana Loki**.
  Example query:

  ```
  {app="minishop"} |= "payment failed"
  ```

### **2. Metrics**

* Expose Prometheus metrics:

  * `request_total`
  * `error_total`
  * `request_duration_seconds`
* Visualize in Grafana:

  * QPS (queries per second)
  * P95 latency
  * Error rate (red line threshold)

### **3. Traces**

* Use **OpenTelemetry + Jaeger**.
* Each `POST /order` should produce a trace with two spans:

  * `CreateOrder`
  * `DeductInventory`
* Jaeger should display the full trace and latency breakdown.

### **4. Bonus Challenge**

* In Prometheus, define an alert rule:

  * Trigger when `error_rate > 10%`
* Optionally simulate a Slack webhook notification.

---

## 💡 Suggested Tech Stack

| Category | Tool                    |
| -------- | ----------------------- |
| Logs     | Loki + Promtail         |
| Metrics  | Prometheus + Grafana    |
| Traces   | Jaeger (or Tempo)       |
| Agent    | OpenTelemetry Collector |

---

## 🧠 Learning Goals

* Understand the **difference between logs, metrics, and traces**
* Learn to instrument Go services for observability
* Visualize system health in Grafana
* Build end-to-end distributed tracing locally

---

## 🐳 Run the Stack

```bash
docker-compose up -d
```

Then open:

* Grafana → [http://localhost:3000](http://localhost:3000)
* Jaeger → [http://localhost:16686](http://localhost:16686)
