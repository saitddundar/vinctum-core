# Vinctum K8s — Quick Start
#
# 1. Create namespace + secrets:
#    kubectl apply -f namespace.yaml
#    kubectl create secret generic vinctum-secrets -n vinctum \
#      --from-literal=jwt-secret=$(openssl rand -hex 32) \
#      --from-literal=postgres-password=$(openssl rand -hex 16) \
#      --from-literal=redis-password=$(openssl rand -hex 16) \
#      --from-literal=postgres-dsn="postgres://vinctum:<postgres-password>@postgres:5432/vinctum?sslmode=require"
#
# 2. Deploy infrastructure:
#    kubectl apply -f infra.yaml
#
# 3. Deploy microservices (in order):
#    kubectl apply -f identity.yaml
#    kubectl apply -f discovery.yaml
#    kubectl apply -f routing.yaml
#    kubectl apply -f transfer.yaml
#    kubectl apply -f gateway.yaml
#
# 4. Or apply everything at once:
#    kubectl apply -f .
#
# Note: secrets.yaml is a TEMPLATE. Do not apply it directly.
# Use kubectl create secret or an operator (Sealed Secrets / ESO).
