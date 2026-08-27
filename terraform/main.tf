resource "azurerm_resource_group" "platform" {
  name     = "rg-platform-sre"
  location = "northeurope"
}

resource "azurerm_virtual_network" "platform" {
  name                = "vnet-platform-sre"
  address_space       = ["10.50.0.0/16"]
  location            = azurerm_resource_group.platform.location
  resource_group_name = azurerm_resource_group.platform.name

  tags = {
    project     = "azure-platform-sre"
    environment = "local"
    managed_by  = "terraform"
  }
}

resource "azurerm_subnet" "aks" {
  name                 = "snet-aks"
  resource_group_name  = azurerm_resource_group.platform.name
  virtual_network_name = azurerm_virtual_network.platform.name
  address_prefixes     = ["10.50.1.0/24"]
}

resource "azurerm_container_registry" "platform" {
  name                = "acrplatformsre"
  resource_group_name = azurerm_resource_group.platform.name
  location            = azurerm_resource_group.platform.location
  sku                 = "Basic"
  admin_enabled       = true

  tags = {
    project     = "azure-platform-sre"
    environment = "local"
    managed_by  = "terraform"
  }

  lifecycle {
    ignore_changes = [
      role_assignment_mode
    ]
  }
}

resource "terraform_data" "aks_platform" {
  triggers_replace = [
    azurerm_subnet.aks.id,
    "aks-platform-sre",
    "Standard_D2_v2",
    "1"
  ]

  provisioner "local-exec" {
    command = <<-EOT
      set -e

      mkdir -p ../.generated

      curl -sf -X PUT \
        "http://localhost:4577/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg-platform-sre/providers/Microsoft.ContainerService/managedClusters/aks-platform-sre?api-version=2024-04-01" \
        -H "Content-Type: application/json" \
        -d '{
          "location": "northeurope",
          "properties": {
            "dnsPrefix": "aks-platform-sre",
            "agentPoolProfiles": [
              {
                "name": "system",
                "count": 1,
                "vmSize": "Standard_D2_v2",
                "vnetSubnetID": "${azurerm_subnet.aks.id}",
                "osType": "Linux",
                "mode": "System"
              }
            ]
          }
        }' > /dev/null

      echo "Waiting for k3s node readiness..."

      for i in $(seq 1 36); do
        CONTAINER=$(docker ps \
          --filter "name=floci-az-aks" \
          --format '{{.Names}}' | head -n1)

        if [ -n "$CONTAINER" ] && \
           docker exec "$CONTAINER" kubectl get nodes --no-headers 2>/dev/null \
             | grep -q ' Ready '; then

          echo "AKS k3s node is Ready: $CONTAINER"

          docker exec -i "$CONTAINER" sh -c \
            'mkdir -p /etc/rancher/k3s && cat > /etc/rancher/k3s/registries.yaml' <<'REGISTRY_EOF'
mirrors:
  "floci-az-acr-registry:5000":
    endpoint:
      - "http://floci-az-acr-registry:5000"
REGISTRY_EOF

          docker restart "$CONTAINER" > /dev/null

          echo "Waiting for k3s after registry configuration..."

          for j in $(seq 1 36); do
            if docker exec "$CONTAINER" kubectl get nodes --no-headers 2>/dev/null \
              | grep -q ' Ready '; then
              echo "k3s is Ready with Floci ACR configured."
              break
            fi

            if [ "$j" -eq 36 ]; then
              echo "k3s did not recover after registry configuration." >&2
              exit 1
            fi

            sleep 5
          done


          HOSTPORT=$(docker port "$CONTAINER" 6443/tcp \
            | awk -F: '/0.0.0.0/ {print $2; exit}')

          docker exec "$CONTAINER" \
            cat /etc/rancher/k3s/k3s.yaml \
            > ../.generated/aks-kubeconfig

          sed -i -E \
            "s#https://127\.0\.0\.1:6443#https://127.0.0.1:$${HOSTPORT}#" \
            ../.generated/aks-kubeconfig

          chmod 600 ../.generated/aks-kubeconfig

          echo "kubeconfig written for host API port $${HOSTPORT}"

          exit 0
        fi

        sleep 5
      done

      echo "AKS k3s node did not become Ready within 3 minutes." >&2
      exit 1
    EOT
  }

  provisioner "local-exec" {
    when = destroy

    command = <<-EOT
      curl -sf -X DELETE \
        "http://localhost:4577/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg-platform-sre/providers/Microsoft.ContainerService/managedClusters/aks-platform-sre?api-version=2024-04-01" \
        || true
    EOT
  }

  depends_on = [
    azurerm_subnet.aks
  ]
}

resource "terraform_data" "cluster_bootstrap" {
  triggers_replace = [
    terraform_data.aks_platform.id,
    terraform_data.postgres_platform.id,
    filesha256("${path.module}/../scripts/bootstrap-cluster.sh"),
    sha256(join("", [
      for f in fileset("${path.module}/../applications/jobs-api/migrations", "*.sql") :
      filesha256("${path.module}/../applications/jobs-api/migrations/${f}")
    ]))
  ]

  provisioner "local-exec" {
    environment = {
      POSTGRES_ADMIN_PASSWORD = var.postgres_admin_password
    }

    command = "${path.module}/../scripts/bootstrap-cluster.sh"
  }

  depends_on = [
    terraform_data.aks_platform,
    terraform_data.postgres_platform
  ]
}
