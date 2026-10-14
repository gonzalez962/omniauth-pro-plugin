# OmniAuth Pro - CPAMC Plugin

**OmniAuth Pro** es un hub universal y comercial de gestión de cuentas y credenciales para **CPAMC (CLI Proxy API Management Center)**.

Permite conectar, rotar y administrar múltiples cuentas de diversos proveedores de IA desde una interfaz unificada dentro del panel de administración de CPAMC.

## Proveedores Soportados

- 🔷 **Google (Gemini 4 Argon)** - Inferencia oficial vía Google AI Studio / API compatible.
- 🌐 **OpenCode Go** - Zen API y catálogo de modelos completo.
- 🟢 **NVIDIA NIM** - Catálogo empresarial con soporte de créditos API.
- 🟣 **NaN Builders** - Inferencia europea de ultra baja latencia.
- ⚡ **Groq Cloud** - LPU ultra rápido (Llama 3.3, Mixtral).
- 🧠 **Cerebras AI** - Inferencia Wafer-Scale a ultra alta velocidad (>1500 t/s).
- 🌪️ **Mistral AI** - Mistral Large, Codestral, Pixtral.
- ⚙️ **OpenAI Compatible (Personalizado)** - Cualquier gateway o servidor local/remoto con especificación OpenAI.

## Compilación e Instalación

Para compilar e instalar directamente en el directorio de plugins de CPAMC:

```bash
make install
```

O utilizando el compilador Go directamente:

```bash
CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -buildmode=c-shared -o omni-auth-pro-linux-amd64.so .
cp omni-auth-pro-linux-amd64.so ~/services/cliproxy/plugins/
```

## Pruebas

```bash
go test -v ./...
```
