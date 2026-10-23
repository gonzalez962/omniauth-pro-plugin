# OmniAuth Pro - CPAMC Plugin

**OmniAuth Pro** es un hub universal y comercial de gestión de cuentas y credenciales para **CPAMC (CLI Proxy API Management Center)**.

Permite conectar, rotar y administrar múltiples cuentas de diversos proveedores de IA desde una interfaz unificada dentro del panel de administración de CPAMC.

## Características Principales

- 🔐 **Multi-Account Hub**: Gestión unificada de múltiples cuentas por proveedor con persistencia segura en disco y enmascaramiento de claves (`key_mask`).
- 🔄 **Descubrimiento de Modelos (Model Discovery)**:
  - **Manual**: Cada tarjeta de cuenta incluye un botón para consultar y visualizar en un modal dedicado los identificadores de modelos reales soportados por el proveedor.
  - **Automático**: Al vincular y guardar una nueva cuenta o clave, se inicia automáticamente el descubrimiento de modelos en el modal sin requerir interacción manual.
  - **Presentación en Modal (S3)**: Los modelos se visualizan en un diálogo modal por cuenta con acciones de cierre y actualización; las tarjetas de cuentas se mantienen limpias e intactas. Incluye protección contra condiciones de carrera ante respuestas tardías de diferentes cuentas.
  - **Caché Persistente en Credenciales (S4)**: Las consultas exitosas persisten la lista de modelos dentro del objeto `model_cache` del JSON de credenciales de la cuenta junto con su timestamp (`updated_at`), preservando todos los campos existentes y permisos de archivo mediante reemplazo atómico. Se decodifican enteros grandes (>2^53) usando `UseNumber` para evitar truncamiento float64. Las consultas subsecuentes se sirven instantáneamente desde la caché local sin llamadas salientes; el botón de actualización (`refresh=true`) actualiza la caché desde el proveedor.
  - **Seguridad y Sincronización en Backend**: Endpoint additive `/v0/management/hub/accounts/models` (GET/POST) que opera del lado del servidor sin exponer credenciales al navegador, soporta parámetro `refresh` explícito, rechaza identificadores inseguros (path traversal), previene resurrección de cuentas eliminadas durante I/O de red, y aplica límites estrictos de tamaño de respuesta (2MB) y timeout. Las secciones críticas de lectura/escritura en disco están serializadas mediante un mutex en memoria para evitar colisiones entre adiciones, eliminaciones y guardado de caché sin retener bloqueos durante llamadas de red. Para modificaciones externas (fuera del proceso), la verificación de identidad de credenciales (API key, base URL y provider) previa al guardado descarta resultados obsoletos sin alterar el archivo ni restaurar datos desactualizados.

## Proveedores Soportados

- 🔷 **Google (Gemini 4 Argon)** - Inferencia oficial vía Google AI Studio / API compatible.
- 🌐 **OpenCode Go** - Zen API y catálogo de modelos completo.
- 🟢 **NVIDIA NIM** - Catálogo empresarial con soporte de créditos API.
- 🟣 **NaN Builders** - Inferencia europea de ultra baja latencia.
- ⚡ **Groq Cloud** - LPU ultra rápido (Llama 3.3, Mixtral).
- 🧠 **Cerebras AI** - Inferencia Wafer-Scale a ultra alta velocidad (>1500 t/s).
- 🌪️ **Mistral AI** - Mistral Large, Codestral, Pixtral.
- ⚙️ **OpenAI Compatible (Personalizado)** - Cualquier gateway o servidor local/remoto con especificación OpenAI.

## 🛠️ Compilación e Instalación

Para utilizar el plugin en CPAMC / CLIProxyAPI se requiere compilarlo como una biblioteca dinámica en C (`.so`, C-ABI). Disponés de dos métodos según tu entorno de trabajo:

### Método A: Compilación directa en el Host (Linux / Ubuntu con Go y GCC)

Si tenés instalado Go 1.22+ y GCC en tu sistema operativo:

1. **Compilar la biblioteca compartida (`.so`)**:
   ```bash
   make build
   ```
   O utilizando el compilador Go directamente:
   ```bash
   CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -buildmode=c-shared -o omni-auth-pro-linux-amd64.so .
   ```

2. **Instalar en el directorio de plugins de CPAMC**:
   ```bash
   make install
   # O copiando manualmente:
   cp omni-auth-pro-linux-amd64.so ~/services/cliproxy/plugins/
   ```

---

### Método B: Compilación dentro de Docker (Cero dependencias en el host)

Si estás en Windows, macOS o preferís no instalar Go ni GCC en tu máquina host, podés compilar dentro de un contenedor efímero oficial de Go. Esto garantiza compatibilidad binaria exacta con la ABI Linux/glibc requerida por el contenedor de CLIProxyAPI / CPAMC.

#### Linux amd64 (Ubuntu / Debian / Docker x86_64 estándar)

- **Linux / macOS (Bash o Zsh)**:
  ```bash
  docker run --rm -v "$(pwd):/src" -w /src golang:1.22 \
    go build -buildmode=c-shared -trimpath -ldflags="-s -w" -o omni-auth-pro-linux-amd64.so .
  ```

- **Windows (Git Bash)**:
  ```bash
  MSYS_NO_PATHCONV=1 docker run --rm -v "$(pwd):/src" -w /src golang:1.22 \
    go build -buildmode=c-shared -trimpath -ldflags="-s -w" -o omni-auth-pro-linux-amd64.so .
  ```

- **Windows (PowerShell)**:
  ```powershell
  docker run --rm -v "${PWD}:/src" -w /src golang:1.22 `
    go build -buildmode=c-shared -trimpath -ldflags="-s -w" -o omni-auth-pro-linux-amd64.so .
  ```

- **Windows (CMD)**:
  ```cmd
  docker run --rm -v "%cd%":/src -w /src golang:1.22 go build -buildmode=c-shared -trimpath -ldflags="-s -w" -o omni-auth-pro-linux-amd64.so .
  ```

#### Linux arm64 (Apple Silicon Docker / Raspberry Pi / AWS Graviton)

Para compilar el binario para arquitectura ARM64:

```bash
docker run --rm --platform linux/arm64 -v "$(pwd)":/src -w /src golang:1.22 \
  go build -buildmode=c-shared -trimpath -ldflags="-s -w" -o omni-auth-pro-linux-arm64.so .
```

---

### 📦 Configuración en Docker Compose

Una vez generado el binario `.so`, montá la carpeta de plugins en tu contenedor de CLIProxyAPI:

```yaml
services:
  cliproxy:
    image: router-for-all/cli-proxy-api:latest
    ports:
      - "8000:8000"
    volumes:
      - ./config.yaml:/app/config.yaml
      - ./plugins:/app/plugins
```

Y configuralo en tu `config.yaml`:

```yaml
plugins:
  enabled: true
  dir: /app/plugins
  configs:
    omni-auth-pro-linux-amd64:
      enabled: true
```

## Pruebas

Para ejecutar las pruebas unitarias localmente (requiere CGO habilitado):

```bash
go test -v ./...
```

O dentro de Docker (sin dependencias en el host):

```bash
docker run --rm -v "$(pwd):/src" -w /src golang:1.22 go test -v ./...
```
*(En Windows Git Bash, anteponé `MSYS_NO_PATHCONV=1` al comando).*

Para ejecutar las pruebas de comportamiento de la interfaz embebida (Node.js built-in test runner):

```bash
node --test ui_models_test.js
```
