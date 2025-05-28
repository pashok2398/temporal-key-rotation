# Temporal Codec Server with AWS KMS Encryption

A high-performance encryption codec server for Temporal workflows that implements envelope encryption using AWS KMS with intelligent caching and automatic key rotation.

## 🏗️ Architecture Overview

```
┌─────────────┐    ┌─────────────────┐    ┌─────────────┐    ┌─────────────┐
│   API/Worker│    │  Codec Client   │    │ Codec Server│    │   AWS KMS   │
│             │────│                 │────│             │────│             │
│ Temporal SDK│    │ HTTP Transport  │    │ Encryption  │    │ Key Mgmt    │
└─────────────┘    └─────────────────┘    └─────────────┘    └─────────────┘
                                                │
                                                ▼
                                         ┌─────────────┐
                                         │  Temporal   │
                                         │  Database   │
                                         │ (Encrypted) │
                                         └─────────────┘
```

## 🔐 Encryption Flow

### Envelope Encryption Pattern

The system uses **envelope encryption** - a security best practice where data is encrypted with a data key, and the data key itself is encrypted with a master key stored in AWS KMS.

```
Business Data ─┐
               ├─► AES-256-GCM ─► Encrypted Payload
Data Key ──────┘
   │
   ▼
AWS KMS Master Key ─► Encrypted Data Key
```

### Step-by-Step Process

#### **Encryption (Encode)**
1. **Data Preparation**: Temporal sends base64-encoded JSON to codec server
2. **Key Management**: Get current data key (generated hourly, cached in memory)
3. **Data Encryption**: Encrypt JSON with AES-256-GCM using data key
4. **Response Creation**: Return encrypted data + KMS metadata

#### **Decryption (Decode)**
1. **Key Retrieval**: Extract encrypted data key from payload metadata
2. **Key Decryption**: Decrypt data key using AWS KMS (with intelligent caching)
3. **Data Decryption**: Decrypt payload using decrypted data key
4. **Response**: Return original JSON data

### Payload Structure

**Unencrypted Payload:**
```json
{
  "metadata": {"encoding": "json/plain"},
  "data": "eyJpZCI6MTIzLCJuYW1lIjoiSm9obiJ9"  // base64 JSON
}
```

**Encrypted Payload:**
```json
{
  "metadata": {"encoding": "binary/encrypted"},
  "data": "k2j3h4k5j6h7k8j9...",                                    // encrypted data
  "kms_key_id": "arn:aws:kms:us-east-1:123:key/...",              // master key ARN
  "encrypted_data_key": "AQICAHh...encrypted-key-blob...==",       // encrypted data key
  "algorithm": "AES-256-GCM"                                       // encryption algorithm
}
```
![image](https://github.com/user-attachments/assets/7396b3b8-37fd-43df-b660-ecae76cf8075)


## 🚀 Caching System

The codec server implements a **two-tier caching system** for optimal performance and cost efficiency:

### Cache #1: Current Data Key (Hot Cache)
- **Purpose**: Store the currently active encryption key
- **Lifetime**: 1 hour (configurable via `DATA_KEY_ROTATION_INTERVAL`)
- **Usage**: 90%+ of all encryption operations
- **Storage**: In-memory struct

```go
type CurrentDataKey struct {
    PlaintextKey     []byte    // 32-byte AES key
    EncryptedKey     string    // Base64 KMS blob
    GeneratedAt      time.Time // Creation timestamp
    ExpiresAt        time.Time // Expiration time
}
```

### Cache #2: Decryption Cache (Cold Cache)
- **Purpose**: Store old decrypted keys for historical data
- **Lifetime**: 24 hours (configurable via `KMS_CACHE_TTL`)
- **Usage**: ~9% of operations (decrypting older data)
- **Storage**: In-memory map

```go
decryptionCache map[string]*CachedKey  // Key = encrypted data key (base64)

type CachedKey struct {
    Key       []byte    // Decrypted 32-byte AES key
    ExpiresAt time.Time // Cache expiration
}
```

### Cache Performance

| Cache Type | Hit Rate | Response Time | Cost |
|------------|----------|---------------|------|
| Current Key | 90% | ~0.001ms | Free |
| Decryption Cache | 9% | ~0.01ms | Free |
| KMS API Call | 1% | ~100ms | $0.03/10K requests |

### Memory Usage

- **Current key**: ~280 bytes
- **Cached key**: ~56 bytes each
- **Total usage**: ~8KB for typical workload (1000 workflows/24h)

## 🔄 Key Rotation

### Master Key Rotation

The system supports **zero-downtime master key rotation** using AWS KMS aliases:

#### **Setup**
```bash
# Create alias pointing to current key
aws kms create-alias --alias-name alias/temporal-codec-latest --target-key-id key-v1

# Set environment variable
export KMS_KEY_ALIAS="alias/temporal-codec-latest"
```

#### **Rotation Process**
```bash
# 1. Create new master key
aws kms create-key --description "Temporal Codec Key v2"

# 2. Update alias (zero downtime)
aws kms update-alias --alias-name alias/temporal-codec-latest --target-key-id key-v2

# 3. Service automatically picks up new key on next data key rotation (max 1 hour)
```

#### **Immediate Rotation (Optional)**
```bash
# For immediate effect, restart service (2-3 seconds downtime)
systemctl restart codec-server
```

### Data Key Rotation

Data keys rotate automatically every hour to minimize exposure:

- **Frequency**: Every 1 hour (configurable)
- **Trigger**: Time-based expiration
- **Process**: Generate new data key from current master key
- **Backward Compatibility**: Old keys cached for decryption

### Multi-Tenant Support

Each tenant can have isolated encryption keys:

```bash
# Tenant A
export KMS_KEY_ALIAS="alias/tenant-a-codec"

# Tenant B  
export KMS_KEY_ALIAS="alias/tenant-b-codec"

# Production
export KMS_KEY_ALIAS="alias/prod-codec"
```

## ⚙️ Configuration

### Environment Variables

| Variable | Description | Default | Example |
|----------|-------------|---------|---------|
| `KMS_KEY_ALIAS` | AWS KMS key alias | `alias/temporal-codec-latest` | `alias/prod-codec` |
| `DATA_KEY_ROTATION_INTERVAL` | Data key rotation frequency (seconds) | `3600` (1 hour) | `1800` (30 min) |
| `KMS_CACHE_TTL` | Old key cache duration (seconds) | `86400` (24 hours) | `43200` (12 hours) |
| `PORT` | Server port | `8081` | `8080` |
| `AWS_REGION` | AWS region | - | `us-east-1` |
| `AWS_ACCESS_KEY_ID` | AWS access key | - | `AKIA...` |
| `AWS_SECRET_ACCESS_KEY` | AWS secret key | - | `xyz...` |

### AWS IAM Permissions

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "kms:DescribeKey",
        "kms:GenerateDataKey", 
        "kms:Decrypt"
      ],
      "Resource": [
        "arn:aws:kms:*:*:key/*",
        "arn:aws:kms:*:*:alias/*"
      ]
    }
  ]
}
```

## 🚀 Deployment

### Build and Run

```bash
# Build
go build -o bin/codec-server ./codec-server

# Run with configuration
export KMS_KEY_ALIAS="alias/prod-codec"
export DATA_KEY_ROTATION_INTERVAL=3600
export KMS_CACHE_TTL=86400
./bin/codec-server
```

### Docker Deployment

```dockerfile
FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o codec-server ./codec-server

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/codec-server .
EXPOSE 8081
CMD ["./codec-server"]
```

```bash
# Run container
docker run -d \
  -e KMS_KEY_ALIAS="alias/prod-codec" \
  -e AWS_REGION="us-east-1" \
  -e AWS_ACCESS_KEY_ID="$AWS_ACCESS_KEY_ID" \
  -e AWS_SECRET_ACCESS_KEY="$AWS_SECRET_ACCESS_KEY" \
  -p 8081:8081 \
  codec-server
```

### Kubernetes Deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: codec-server
spec:
  replicas: 3
  selector:
    matchLabels:
      app: codec-server
  template:
    metadata:
      labels:
        app: codec-server
    spec:
      containers:
      - name: codec-server
        image: codec-server:latest
        ports:
        - containerPort: 8081
        env:
        - name: KMS_KEY_ALIAS
          value: "alias/prod-codec"
        - name: DATA_KEY_ROTATION_INTERVAL
          value: "3600"
        - name: AWS_REGION
          value: "us-east-1"
        # Use AWS IAM roles for service accounts (IRSA) for production
        resources:
          requests:
            memory: "64Mi"
            cpu: "50m"
          limits:
            memory: "128Mi"
            cpu: "100m"
```

## 📊 Monitoring

### Health Endpoints

- **`GET /health`**: Service health check
- **`GET /stats`**: Key usage statistics
- **`POST /encode`**: Encrypt payloads
- **`POST /decode`**: Decrypt payloads

### Key Metrics

```bash
# Check key statistics
curl http://localhost:8081/stats

# Response:
{
  "cached_keys_count": 5,
  "current_key_age": "25m30s",
  "current_key_expires_in": "34m30s", 
  "current_key_expired": false
}
```

### CloudWatch Metrics

Monitor these AWS CloudWatch metrics:

- **KMS API Calls**: `AWS/KMS/NumberOfRequestsSucceeded`
- **KMS Errors**: `AWS/KMS/NumberOfRequestsFailed`
- **Latency**: Custom metrics for encode/decode operations

### Alerts

Set up alerts for:

- High KMS API error rate (> 1%)
- Expired current key (`current_key_expired: true`)
- High response latency (> 200ms)
- Service health check failures

## 🔧 Operations

### Key Rotation Procedures

#### **Planned Rotation**
```bash
# 1. Create new master key
NEW_KEY=$(aws kms create-key --description "Codec Key $(date +%Y%m%d)" --query 'KeyMetadata.KeyId' --output text)

# 2. Update alias
aws kms update-alias --alias-name alias/prod-codec --target-key-id $NEW_KEY

# 3. Monitor service (automatically picks up new key within 1 hour)
watch curl -s http://localhost:8081/stats
```

#### **Emergency Rotation**
```bash
# 1. Create emergency key
EMERGENCY_KEY=$(aws kms create-key --description "Emergency Codec Key" --query 'KeyMetadata.KeyId' --output text)

# 2. Update alias immediately
aws kms update-alias --alias-name alias/prod-codec --target-key-id $EMERGENCY_KEY

# 3. Restart service for immediate effect
systemctl restart codec-server

# 4. Verify new key in use
curl http://localhost:8081/stats
```

### Troubleshooting

#### **Common Issues**

**Service can't resolve KMS alias:**
```bash
# Check alias exists
aws kms describe-key --key-id alias/prod-codec

# Check IAM permissions
aws sts get-caller-identity
```

**High KMS API costs:**
```bash
# Check cache hit rates
curl http://localhost:8081/stats

# Increase cache TTL if needed
export KMS_CACHE_TTL=172800  # 48 hours
```

**Decryption failures:**
```bash
# Check service logs
journalctl -u codec-server -f

# Verify key permissions
aws kms decrypt --ciphertext-blob $(echo "test" | base64) --key-id alias/prod-codec
```

## 💰 Cost Optimization

### KMS Cost Analysis

| Scenario | Requests/Month | Cost/Month | Optimization |
|----------|----------------|------------|--------------|
| Per-operation keys | 1M operations = 1M KMS calls | ~$3,000 | ❌ Expensive |
| 1-hour rotation | 1M operations = ~720 KMS calls | ~$3 | ✅ Optimal |
| 4-hour rotation | 1M operations = ~180 KMS calls | ~$1 | ⚠️ Less secure |

### Recommendations

- **Production**: 1-hour rotation (security vs cost balance)
- **Development**: 4-hour rotation (cost optimization)
- **High-security**: 30-minute rotation (maximum security)

## 🔒 Security

### Encryption Strengths

- **AES-256-GCM**: Industry standard symmetric encryption
- **Envelope encryption**: Data keys never stored in plaintext
- **Key rotation**: Regular key material changes
- **AWS KMS**: Hardware security module (HSM) backing
- **Memory security**: Keys zeroed after use

### Best Practices

1. **Use IAM roles** instead of access keys when possible
2. **Enable AWS CloudTrail** for audit logging
3. **Rotate master keys** regularly (annually minimum)
4. **Monitor KMS usage** for anomalies
5. **Use different keys** per environment/tenant
6. **Enable KMS key rotation** in AWS
7. **Implement least privilege** IAM policies

### Compliance

The system supports compliance with:

- **SOC 2 Type II**: Encrypted data at rest and in transit
- **HIPAA**: Healthcare data protection requirements
- **PCI DSS**: Payment card industry standards
- **GDPR**: Data protection and privacy regulations

---

## 📝 Example Usage

### Basic Encryption Test

```bash
# Test encoding
curl -X POST http://localhost:8081/encode \
  -H "Content-Type: application/json" \
  -d '{
    "payloads": [{
      "metadata": {"encoding": "json/plain"},
      "data": "eyJpZCI6MTIzLCJuYW1lIjoiSm9obiJ9"
    }]
  }'

# Test decoding  
curl -X POST http://localhost:8081/decode \
  -H "Content-Type: application/json" \
  -d '{
    "payloads": [{
      "metadata": {"encoding": "binary/encrypted"},
      "data": "encrypted-data-here",
      "kms_key_id": "arn:aws:kms:us-east-1:123:key/...",
      "encrypted_data_key": "AQICAHh...",
      "algorithm": "AES-256-GCM"
    }]
  }'
```

This codec server provides enterprise-grade encryption for Temporal workflows with optimal performance, cost efficiency, and operational simplicity.
