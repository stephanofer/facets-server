# Project Instructions

## No negociables

- Este proyecto se enfoca exclusivamente en el cerebro del finance tracker: el servidor/backend. Aquí se implementan y verifican las reglas financieras, la persistencia, la seguridad y los contratos API, y se deja la documentación necesaria para su integración. Otro equipo desarrollará el primer cliente, la aplicación Android, en un proyecto separado. No desarrollamos el cliente Android aquí ni condicionamos el cierre de los bloques del backend a su implementación; las referencias a Android describen al consumidor del servidor, no tareas de este equipo.
- Quiero que el proyecto tenga la menor sobreingeniería posible. Queremos algo funcional,sin errores, que se mantenga simple en el sentido de no complicar las cosas innesesariametne, que sea escalable, fácil de mantener, auditable y depurable; es decir, no deberíamos introducir complejidad innecesaria que nos vaya a causar problemas más adelante. Nada de problemas de rendimiento, nada de ineficiencias; queremos algo con una ultra-performance y sobre todo eficiente y no tener comportamietnos extraños. Ojo esto no queirie decir limitarnos en ninguna manera, queremos tener el mejor proyecto pero mi punto esque no quermeos hacer y agregar complejidad innesesaria sin motivo y como te digo esto no es limitarnos todo lo contrario es hacer un mejor proyecto
- Recuerda que el entorno de trabajo actual que esta trabajndo todo el equipo de trabajo es en powershell entonces todo lo que es terminal es powershell
- Bien tambien recuerda que no debes delegar a subagentes a menos que el usuario te lo indique si no te lo indica no delegues
- Mantén `README.md` breve: solo descripción del proyecto, comandos esenciales y referencias importantes; no lo uses como documentación detallada ni para seguir avances.

## Skill routing

- Carga únicamente las skills necesarias para la tarea actual. No cargues todo el catálogo, skills por precaución ni referencias cruzadas automáticamente; cada carga debe responder a una necesidad concreta. Lee recursos adicionales solo cuando hagan falta y no recargues instrucciones ya presentes en la sesión.
- Para tareas Go, usa `golang-how-to` como enrutador y selecciona solo las especialidades aplicables de esta tabla. Las decisiones del proyecto, esta selección mínima y la prohibición de delegar sin autorización del usuario prevalecen sobre las recomendaciones genéricas de las skills. Una skill no autoriza agregar dependencias ni ampliar el alcance.
- Catálogo local: `.agents/skills/<skill>/SKILL.md`. No asumas instaladas las skills externas mencionadas en sus referencias.

| Skill | Cuándo cargarla |
| --- | --- |
| `golang-how-to` | Al abordar código, revisión, depuración o configuración Go; selecciona las especialidades necesarias. |
| `use-modern-go` | Antes de escribir, modificar, corregir o refactorizar código Go; consulta su CLI para la versión del proyecto. |
| `golang-benchmark` | Al escribir/comparar benchmarks, medir rendimiento o interpretar perfiles CPU/memoria y trazas. |
| `golang-code-style` | Al escribir o revisar Go para resolver legibilidad, declaraciones, flujo de control y organización del código. |
| `golang-concurrency` | Al diseñar o revisar goroutines, canales, sincronización, workers o acceso compartido en memoria. |
| `golang-context` | Al diseñar o corregir propagación de contexto, cancelación, plazos o valores; no solo porque una función reciba `ctx`. |
| `golang-continuous-integration` | Al configurar GitHub Actions, controles CI/CD, releases o bots de actualización; no para analizar sus hallazgos. |
| `golang-data-structures` | Al elegir colecciones, analizar slices/maps, copias o memoria, o implementar contenedores genéricos. |
| `golang-database` | Al escribir, revisar o depurar acceso SQL desde Go, transacciones, bloqueos, pools o pruebas de persistencia; no genera esquemas. |
| `golang-dependency-injection` | Al conectar dependencias y sus ciclos de vida, diseñar constructores para inyección o desacoplar servicios. |
| `golang-dependency-management` | Al agregar/quitar/actualizar dependencias o herramientas Go, resolver versiones o auditar módulos con `govulncheck`. |
| `golang-design-patterns` | Al decidir patrones de arquitectura, constructores, opciones, cierre ordenado, reintentos o gestión de recursos. |
| `golang-documentation` | Al escribir o revisar documentación técnica Go, comentarios godoc, ejemplos o documentación API; respeta el README mínimo. |
| `golang-error-handling` | Al crear, propagar, envolver, clasificar o registrar errores; también al decidir sobre panic/recover. |
| `golang-gopls` | Al necesitar definiciones, referencias, tipos, diagnósticos o refactorizaciones semánticas del código local y sus dependencias resueltas. |
| `golang-lint` | Al configurar `.golangci.yml`, interpretar/corregir avisos de lint, vet o staticcheck, o justificar `nolint`. |
| `golang-naming` | Al elegir o revisar nombres de paquetes, tipos, funciones, interfaces, errores, variables o pruebas. |
| `golang-observability` | Al instrumentar o revisar logs estructurados, métricas, trazas, alertas o perfiles continuos de producción. |
| `golang-performance` | Al optimizar un cuello de botella medido o revisar rendimiento; para medir, usa `golang-benchmark`. |
| `golang-pkg-go-dev` | Al consultar documentación publicada, APIs, versiones, licencias o vulnerabilidades de paquetes Go; no para símbolos locales. |
| `golang-popular-libraries` | Al comparar/elegir bibliotecas o evaluar si la biblioteca estándar basta; no para usar una API ya elegida. |
| `golang-project-layout` | Al crear o reorganizar directorios, paquetes, módulos o workspaces Go; no para refactorizaciones sin cambio de estructura. |
| `golang-refactoring` | Al reestructurar código existente preservando comportamiento: extraer, mover, renombrar a escala o romper ciclos. |
| `golang-safety` | Al prevenir nil panics, aliasing, desbordamientos, conversiones inseguras o mal manejo de recursos; no para amenazas de seguridad. |
| `golang-samber-lo` | Al trabajar con `samber/lo` o evaluar transformaciones funcionales de colecciones; no obliga a adoptar la biblioteca. |
| `golang-samber-oops` | Al escribir o revisar errores estructurados, atributos, trazas o integración de logs con `samber/oops`. |
| `golang-security` | Al tocar autenticación, permisos, criptografía, secretos, entradas no confiables o fronteras de archivos/red, o auditar vulnerabilidades del código. |
| `golang-structs-interfaces` | Al diseñar structs/interfaces, composición, embedding, tags, aserciones de tipo o receptores por valor/puntero. |
| `golang-testing` | Al escribir/revisar pruebas, fixtures, mocks, fuzzing, cobertura, aislamiento o investigar pruebas inestables. |
| `golang-troubleshooting` | Ante fallos reales, bloqueos, carreras o comportamiento inesperado; primero reproducir y encontrar la causa. |


## Referencias

- **Bruno:** antes de tocar `api-client/`, consulta `C:\Users\vendi\Documents\software-workspace\facets\bruno-docs`. Todo endpoint nuevo, modificado o eliminado debe reflejarse correctamente en su request y scripts/aserciones, listo para probar en Bruno; verifica con `api-client-check` y usa `api-client-smoke` para el recorrido smoke (requiere CLI y servidor local).
- **Documentación de Huma:** consulta los Markdown de `docs/huma/` antes de implementar, modificar o revisar código que dependa de Huma. Lee las secciones pertinentes a la tarea; no adivines APIs, validaciones, contratos ni comportamientos.
- **Código fuente de Huma:** disponible en `C:\Users\vendi\Documents\software-workspace\facets\huma`. Si necesitas confirmar cómo está implementado un comportamiento o la documentación no basta, revisa el código y sus pruebas relevantes. Comprueba que correspondan a la versión usada en `go.mod`; ante diferencias, verifica la implementación de esa versión. Es material de consulta, no un destino de cambios de este proyecto.
- **Sure como inspiración de backend:** este proyecto se inspira en Sure, cuyo código local está en `inspiration/sure/`, pero tiene un alcance más específico y busca evitar complejidad innecesaria. Al diseñar o implementar funcionalidades de nuestro alcance, consulta las soluciones equivalentes de Sure: lógica de negocio, límites de autorización, modelo de datos, migraciones y pruebas. Busca aprovechar problemas ya resueltos y comprender sus decisiones para anticipar casos límite y evitar errores de diseño, no reproducir su producto completo.
  - Verifica cada conclusión en el código y las pruebas pertinentes; no supongas que una solución es correcta o adecuada solo porque Sure la utiliza. Distingue lo comprobado de lo inferido, filtra lo útil y adapta las decisiones a nuestras reglas financieras, casos de uso, stack Go/PostgreSQL y requisitos de simplicidad, seguridad y eficiencia. Señala las rutas consultadas al justificar decisiones relevantes.
  - Limita la consulta a backend y funcionalidades que realmente necesitamos: no traslades clientes Android/iOS/web, features ajenas al alcance, dependencias ni abstracciones de su stack. No copies a ciegas código o esquemas; antes de reutilizar material, verifica su licencia. Sure es una referencia de lectura, no una dependencia ni un destino de cambios de este proyecto.

## Comandos de desarrollo y verificación

Usa `./scripts/dev.ps1 <tarea>` en PowerShell. El script selecciona Go desde `environment.json`; sin tarea ejecuta `validate`.

| Tarea | Qué hace / cuándo usarla |
| --- | --- |
| `setup` | Prepara herramientas e imágenes; crea `.env` si falta o está vacío. Para preparar el entorno. |
| `doctor` | Informa versiones y comprueba Docker Linux. Para diagnosticar el entorno. |
| `db-up` | Inicia PostgreSQL local y espera que esté listo. Antes de usar la base local. |
| `db-down` | Detiene Compose sin borrar el volumen de datos. Al dejar de usar la base local. |
| `db-config` | Genera credenciales/clave faltantes, configura roles, migra y aplica permisos locales. Para provisionar la base. |
| `migrate` | Aplica migraciones locales con el rol migrador. Tras agregar migraciones; no se aplican al arrancar el servidor. |
| `generate` | Regenera sqlc y `docs/openapi.json`. Tras cambiar consultas, esquemas o contratos API. |
| `openapi` | Regenera solo `docs/openapi.json`. Tras cambiar el contrato API. |
| `api-client-check` | Comprueba sin CLI ni servidor la sincronización de Bruno con OpenAPI; también se ejecuta en `check` y `validate`. |
| `api-client-smoke` | Ejecuta el smoke secuencial de Bruno contra el servidor local; requiere CLI y cliente configurado/verificado. Opcional, fuera de `validate`. |
| `format` | Formatea archivos Go con `gofmt`. Después de un conjunto de cambios Go. |
| `check` | Comprueba formato y ejecuta pruebas sin etiqueta de integración, permitiendo caché. Para iterar rápido. |
| `test` | Ejecuta pruebas sin etiqueta de integración ni caché de resultados; límite de 2 min por paquete. |
| `integration` | Incluye pruebas de integración con PostgreSQL temporal; sin caché, límite de 5 min por paquete. Requiere Docker Linux. |
| `race` | Ejecuta pruebas con `-race` en Docker Linux con Go/GCC; sin integración, límite de 5 min por paquete. Detecta carreras en memoria. |
| `lint` | Ejecuta el análisis estático/seguridad definido en `.golangci.yml`. Para revisar cambios Go. |
| `build` | Compila en `artifacts/`, inspecciona el binario y ejecuta su modo de versión. Para verificar el artefacto. |
| `run` | Carga configuración local e inicia el servidor HTTP. Para probar la API localmente. |
| `worker` | Carga configuración local e inicia el worker. Para procesar trabajos de la cola configurada. |
| `validate` | Valida código: entorno, formato, script, pruebas, tidy, sqlc/OpenAPI, migraciones, integración, vet/lint, race, vulnerabilidades y build. |

- Durante cambios Go: usa `format` y `check -Packages ./internal/<paquete>`; `test` también acepta `-Packages`. En `check`, el filtro solo limita las pruebas, no la revisión de formato.
- Ejecuta `integration` para cambios de persistencia/transacciones y `race` para concurrencia en memoria; ninguno sustituye al otro. Integración usa bases temporales, no `.env` ni la base local.
- Durante el desarrollo prioriza las comprobaciones focalizadas, no `validate` tras cada edición. Si una etapa falla, reproduce y corrige con esa prueba/comando antes de solicitar otra validación completa.
- Utiliza eficientemente `validate` en los momentso que sean necesarios no en todo momento osea se eficeinte al utilizarlo.
- `check` no sustituye a `validate`. Si una verificación falla o no puede ejecutarse, informa el motivo; no declares el cierre validado. Revisa las diferencias regeneradas si `validate` detecta archivos desactualizados.
