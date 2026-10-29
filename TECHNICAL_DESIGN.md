# Diseño técnico del backend

**Estado:** base acordada para implementación, evolutiva.  
**Fecha de referencia:** 29 de octubre de 2026.  
**Documentos relacionados:** [diseño de producto](PRODUCT_DESIGN.md) e [instrucciones del proyecto](AGENTS.md).

## 1. Propósito y evolución

Este documento establece las decisiones técnicas y las precisiones de negocio acordadas para construir el backend. Complementa `PRODUCT_DESIGN.md`; no sustituye su alcance ni reproduce todas sus funcionalidades.

**El diseño no está escrito en piedra.** Cualquier decisión, dependencia, versión, organización o regla puede corregirse, simplificarse o reemplazarse cuando exista sustento técnico o de producto. Descubrir una omisión o una decisión equivocada durante el desarrollo es motivo para revisar el diseño, no para implementar un comportamiento incorrecto por obedecerlo.

Cada cambio debe registrar qué reemplaza, su motivo, sus consecuencias y, cuando corresponda, la migración de datos, compatibilidad y pruebas necesarias. Se actualizan los documentos afectados junto con la implementación; no se mantienen reglas contradictorias ni se cambia silenciosamente el significado de cifras históricas.

Las precisiones de este documento sobre salida de integrantes, reconocimiento de servicios, visibilidad, calendario, soporte y retención resuelven los puntos abiertos correspondientes de `PRODUCT_DESIGN.md` (apartados 6.7, 13, 15–16, 19 y 22). Su aplicación queda definida aquí; cualquier discrepancia adicional exige revisión explícita, no una elección silenciosa del desarrollador.

## 2. Principios obligatorios

- Una sola verdad financiera: operaciones relacionadas explican cuentas, deudas, reportes, presupuestos y asistente.
- Consistencia inmediata de efectos financieros confirmados; una operación compuesta se confirma completa o no se confirma.
- Registro bajo control humano: fechas, estimaciones y automatizaciones no crean movimientos ni marcan pagos.
- Privacidad por espacio, cuenta y recurso, aplicada antes de calcular agregados.
- Incertidumbre explícita: desconocido, estimado y confirmado son estados diferentes; desconocido no significa cero.
- Historia conservada con correcciones trazables y proyecciones reconstruibles.
- SQL, dependencias y flujo de ejecución explícitos; complejidad solo cuando resuelva una necesidad demostrable.
- Recursos acotados, recuperación comprobable y rendimiento medido, no supuesto por la elección del lenguaje.
- Backend como autoridad financiera. Android presenta resultados y valida captura, pero no mantiene fórmulas financieras independientes.

## 3. Tecnologías y dependencias

### 3.1. Base de ejecución y persistencia

Las versiones son la línea base verificada para este diseño, no una instrucción de permanecer indefinidamente en ellas.

| Componente | Versión base | Uso y referencia |
| --- | --- | --- |
| Go | 1.27.3 | Lenguaje y toolchain; [publicaciones oficiales](https://go.dev/dl/). |
| `net/http`, `http.ServeMux` | Incluidos en Go | Servidor, routing y middleware; [referencia](https://pkg.go.dev/net/http). |
| Huma | 2.40.0 | API tipada, validación estructural y OpenAPI; [publicación](https://github.com/danielgtaylor/huma/releases/tag/v2.40.0). |
| PostgreSQL | 18.6 | Fuente de verdad relacional y transaccional; [versionado y soporte](https://www.postgresql.org/support/versioning/). |
| `github.com/jackc/pgx/v5` | 5.11.0 | Driver y `pgxpool`, del mismo módulo; [referencia](https://pkg.go.dev/github.com/jackc/pgx/v5@v5.11.0). |
| `github.com/sqlc-dev/sqlc` | 1.31.1 | Generación Go desde SQL; [integración con pgx](https://docs.sqlc.dev/en/v1.31.1/guides/using-go-and-pgx.html). |
| `github.com/pressly/goose/v3` | 3.29.0 | Migraciones SQL; [referencia](https://pkg.go.dev/github.com/pressly/goose/v3@v3.29.0). |
| `github.com/cockroachdb/apd/v3` | 3.2.3 | Decimales para tasas y cálculos intermedios; [referencia](https://pkg.go.dev/github.com/cockroachdb/apd/v3@v3.2.3). |
| `int64` / `BIGINT` | Nativos | Importes efectivos en unidades menores, con moneda y controles de rango. |

Huma utiliza el adaptador oficial [`humago`](https://pkg.go.dev/github.com/danielgtaylor/huma/v2@v2.40.0/adapters/humago). No se agrega otro router. La versión 2.40.0 reemplaza la propuesta inicial 2.39.1 por sus correcciones de seguridad, validación y límites HTTP, no solo por ser más reciente.

### 3.2. Seguridad, errores y observabilidad

| Componente | Versión base | Uso y referencia |
| --- | --- | --- |
| `golang.org/x/crypto` | 0.58.0 | Argon2id mediante `argon2`; [referencia](https://pkg.go.dev/golang.org/x/crypto@v0.58.0/argon2). |
| `github.com/pquerna/otp` | 1.5.0 | TOTP estándar; [referencia](https://pkg.go.dev/github.com/pquerna/otp@v1.5.0/totp). |
| `crypto/rand`, `crypto/sha256`, `crypto/subtle`, `crypto/aes`, `crypto/cipher` | Incluidos en Go | Tokens, verificadores, comparación segura y cifrado autenticado; sin algoritmos propios. |
| `log/slog` | Incluido en Go | Logs JSON estructurados; [referencia](https://pkg.go.dev/log/slog). |
| `github.com/samber/oops` | 1.23.2 | Contexto, códigos y stack de errores técnicos; [referencia](https://pkg.go.dev/github.com/samber/oops@v1.23.2). |
| `go.opentelemetry.io/otel` y SDK correspondiente | 1.47.0 | Trazas y métricas; [publicación](https://github.com/open-telemetry/opentelemetry-go/releases/tag/v1.47.0). |

`oops` no define las reglas del dominio ni autoriza a registrar solicitudes completas. Los errores de negocio tienen códigos estables; los errores técnicos reciben contexto permitido y se traducen en la frontera HTTP.

Los módulos auxiliares de OpenTelemetry tienen versiones propias: se fijan y comprueban al incorporar cada instrumentación o exportador; no se les asigna automáticamente la versión del núcleo.

### 3.3. Pruebas y herramientas

| Componente | Versión base | Uso y referencia |
| --- | --- | --- |
| `testing`, `httptest`, fuzzing y benchmarks | Incluidos en Go | Pruebas y medición; [referencia](https://pkg.go.dev/testing). |
| Testcontainers Go y módulo PostgreSQL | 0.44.0 | Integración con PostgreSQL real; [núcleo](https://pkg.go.dev/github.com/testcontainers/testcontainers-go@v0.44.0) y [módulo](https://pkg.go.dev/github.com/testcontainers/testcontainers-go/modules/postgres@v0.44.0). |
| golangci-lint | 2.15.0 | Análisis estático; [publicación](https://github.com/golangci/golangci-lint/releases/tag/v2.15.0). |
| Docker Engine | 29.9.2 | Contenedores Linux de desarrollo y pruebas; [publicación](https://github.com/moby/moby/releases/tag/docker-v29.9.2). |
| Docker Compose | 5.6.1 | Dependencias locales reproducibles; [publicación](https://github.com/docker/compose/releases/tag/v5.6.1). |

En el bloque 1 se fija además `golang.org/x/vuln/cmd/govulncheck` 1.9.0 para la validación de vulnerabilidades prevista en 19.3. Las herramientas Go se ejecutan mediante directivas `tool` versionadas; no se enlazan al ejecutable del producto. `environment.json` registra las imágenes con digest: PostgreSQL `18.6-trixie` (también en Compose), Go `1.27.3-trixie` para `-race` sin compilador C global en Windows y Ryuk `0.14.0` para limpieza de Testcontainers. Los fixtures de Goose/sqlc del bloque 1 pertenecen solo a pruebas; no adelantan el esquema financiero ni el arranque del bloque 2.

En Windows se utilizará un entorno compatible con contenedores Linux. Las tareas del equipo serán ejecutables desde PowerShell, sin exigir Make ni utilidades Unix.

`github.com/samber/lo` 1.53.0 es una opción evaluada, **no una dependencia inicial obligatoria** ([referencia](https://pkg.go.dev/github.com/samber/lo@v1.53.0)). Se prefiere biblioteca estándar y bucles claros; solo se incorpora si mejora código concreto, sin asumir ventajas de rendimiento.

### 3.4. Política de versiones

- Registrar dependencias directas y transitivas en `go.mod` y `go.sum`; versionar ambos.
- Fijar toolchain, herramientas e imágenes de pruebas. Usar versiones y digests, no etiquetas `latest` en automatización reproducible.
- Utilizar directivas `tool` para ejecutables Go cuando corresponda; no añadir herramientas al ejecutable del producto.
- Antes de incorporar o actualizar: revisar compatibilidad, mantenimiento, licencia y vulnerabilidades; ejecutar pruebas y revisar cambios de comportamiento.
- Fijar también las versiones de herramientas adicionales al introducirlas. La disponibilidad publicada no equivale a compatibilidad certificada del conjunto.
- No instalar dependencias opcionales ni construir adaptadores especulativos.

## 4. Arquitectura y organización

### 4.1. Monolito modular

Un repositorio, un módulo Go y una base PostgreSQL. API, trabajos, administración interna y migraciones pueden tener puntos de entrada diferentes sobre la misma base de código y versión. No se comunican por HTTP entre sí para ejecutar reglas internas.

El flujo normal es: **entrada → autorización → caso de uso → transacción → resultado**.

Se distinguen entradas/salidas, coordinación de casos de uso y reglas deterministas. Esto no obliga a crear capas, interfaces o paquetes repetidos por cada entidad.

| Área | Propiedad de las reglas |
| --- | --- |
| Identidad y acceso | Credenciales propias, factores, sesiones, preferencias y recuperación. |
| Espacios | Membresías, invitaciones, roles, propiedad y configuración. |
| Finanzas | Cuentas, operaciones, posiciones, reconciliaciones, acuerdos, deudas, cobros y tarjetas. |
| Planificación | Recurrentes, vencimientos, aplicaciones, presupuestos, reservas y proyecciones. |
| Reportes | Consultas financieras, alcance autorizado y calidad de datos. |
| Contratación | Estado y capacidades por espacio, separados de las finanzas registradas. |
| Documentos e incorporación | Archivos, antecedentes, importaciones y exportaciones. |
| Asistente | Conversaciones, herramientas de consulta y propuestas confirmables. |
| Administración interna | Facultades del personal, casos de soporte y acciones restringidas. |

Son fronteras de responsabilidad, no nueve servicios ni una obligación de crear todos los paquetes de inmediato. El núcleo financiero permanece cohesionado; un pago no requiere coordinación eventual entre subsistemas de saldo, deuda y agenda.

### 4.2. Dependencias y convenciones

- Inyección manual mediante constructores; sin contenedor de DI, localizadores de servicios, conexiones globales o inicialización oculta.
- Interfaces pequeñas donde exista una frontera útil: persistencia, correo, archivos, identidad del actor, reloj o servicios externos. No una interfaz por tabla.
- Cada módulo controla sus escrituras. Los casos de uso compuestos comparten una transacción; no eluden reglas modificando tablas ajenas directamente.
- Los reportes pueden consultar varias áreas con SQL especializado y permisos consistentes.
- `cmd/`: puntos de entrada pequeños. `internal/`: implementación del producto organizada por negocio, no por carpetas globales de controladores y repositorios.
- Consultas SQL próximas al área que las mantiene; migraciones con secuencia global. Código generado identificado y no editable manualmente.
- Tests próximos al código; fixtures en `testdata/`. Identificadores y nombres técnicos en inglés, con convenciones idiomáticas de Go.
- Evitar paquetes genéricos `utils`, `helpers` o `common`. Extraer paquetes por responsabilidad, no por cantidad de líneas.
- Separar contratos HTTP de tipos de persistencia cuando difieran. Las estructuras de sqlc no son automáticamente la API pública.
- No introducir ORM, repositorio CRUD universal, event sourcing, bus genérico, microservicios ni caché financiera compartida inicialmente.

## 5. Núcleo financiero

### 5.1. Operación y efectos

La unidad de escritura es una operación financiera con identidad estable, tipo, fecha efectiva, momento de registro, autor, origen y revisión vigente. Relaciona:

- Efectos sobre posiciones financieras en su moneda.
- Componentes de ingreso o gasto, cuando existen.
- Aplicaciones a obligaciones.
- Clasificación, referencias cambiarias, documentos y antecedentes pertinentes.

No se representan las partes de un mismo hecho como movimientos independientes que puedan confirmarse o corregirse parcialmente. Formularios, importaciones e IA utilizan los mismos casos de uso.

Se adopta un **registro de efectos tipados e invariantes por operación**, no un plan contable empresarial completo. Cada tipo define sus efectos válidos; una tabla genérica de cantidades con signo no basta. No se suman monedas distintas para comprobar equilibrio.

| Hecho | Posiciones | Ingreso o gasto |
| --- | --- | --- |
| Sueldo cobrado | Aumenta efectivo | Ingreso. |
| Compra con efectivo o débito | Disminuye efectivo | Gasto. |
| Compra con tarjeta o financiación | Aumenta obligación | Gasto una vez. |
| Transferencia interna | Cambia ambas cuentas | Ninguno; comisión separada como gasto. |
| Pago de tarjeta | Disminuyen efectivo y obligación | No repite la compra. |
| Préstamo recibido | Aumentan efectivo y deuda | No es ingreso ganado. |
| Amortización de capital | Disminuyen efectivo y capital conocido | No es consumo. |
| Préstamo otorgado y recuperación | Cambian efectivo y derecho de cobro | El capital no es gasto ni ingreso ganado. |
| Intereses, seguros y cargos | Según el hecho y aplicación | Se reconocen una vez, no nuevamente al cancelar algo ya reconocido. |
| Ajuste de reconciliación | Corrige posición | Diferencia identificada, no ingreso/gasto ordinario inventado. |

La lectura de efectivo procede de efectos sobre cuentas líquidas; ingresos/gastos, de componentes económicos; planificación, de expectativas y aplicaciones. No son registros editables independientes de la misma cifra.

### 5.2. Saldos y antecedentes

- Cada posición pertenece a un espacio y una moneda. Una deuda o cobro no duplica su saldo por aparecer en varias vistas.
- Saldo = referencia inicial fechada + efectos vigentes aplicables dentro del seguimiento.
- La referencia inicial no es ingreso/gasto. Antecedentes ya incluidos no vuelven a afectar el saldo actual.
- El saldo actual se mantiene como proyección reconstruible, actualizada en la misma transacción de la operación; nunca se edita directamente.
- Los históricos se calculan desde referencias y efectos correspondientes. Deben existir reconstrucción y comprobación de consistencia; las discrepancias generan una incidencia, no una reparación silenciosa.
- Se conservan saldos negativos, fecha de referencia y calidad del dato. No se muestra precisión confirmada con componentes incompletos.
- Archivar o inmovilizar impide uso cotidiano, no elimina historia. Cambiar moneda con historial exige proceso controlado, no una edición de etiqueta.
- Una billetera sin saldo independiente no crea otra posición de dinero.

### 5.3. Correcciones y reconciliación

Cambios descriptivos o de clasificación se auditan; cambios de importe, fecha o cuenta generan una revisión financiera trazable. Solo la revisión vigente aporta efectos. Anular recalcula todas las posiciones y aplicaciones afectadas dentro de la misma transacción.

La fecha de corrección no reemplaza la fecha efectiva del hecho. Corregir enero en octubre no inventa un flujo de efectivo en octubre. Devoluciones, condonaciones y refinanciamientos reales son hechos nuevos, no correcciones encubiertas.

La reconciliación conserva saldo calculado, observado, diferencia, fecha, autor y motivo. Un ajuste no reescribe silenciosamente la referencia inicial. Si una corrección posterior afecta un saldo reconciliado, se conserva la evidencia y se marca la revisión necesaria.

Las divisiones entre categorías suman el total y afectan una sola vez el saldo. Las devoluciones reducen gasto neto con su fecha real. Los adelantos recuperables separan consumo propio y derecho de cobro. La excepcionalidad es una marca: permanece en gasto y presupuesto por defecto; cualquier exclusión analítica es explícita.

## 6. Dinero y multimoneda

- Importes efectivos en `int64`/`BIGINT`, siempre acompañados de moneda y escala. No asumir dos decimales para todas las monedas.
- API: cantidades en unidades menores como cadenas, por ejemplo `{"amount_minor":"12000","currency":"PEN"}`. Tasas y decimales también como cadenas; no pasar por `float64`.
- Comprobar desbordamientos en sumas, restas, multiplicaciones, conversiones y acumulados. `SUM(BIGINT)` en PostgreSQL devuelve `NUMERIC`; convertirlo exige control de rango.
- Rechazar precisión inválida en importes efectivos, sin truncar ni redondear silenciosamente la captura.
- `apd` con contexto de precisión, condiciones de error y redondeo explícitos. Redondeo a par como base para equivalentes informativos; al final del cálculo, salvo una regla financiera específica documentada.
- Distribuir residuos de divisiones de manera determinista y conservar la suma original.
- Distinguir precio del comercio, importe aplicado a la cuenta y equivalente de presentación. Solo el aplicado cambia esa posición.
- Cada referencia cambiaria conserva dirección, monedas, tasa positiva, fecha, origen, precisión y regla utilizada. Se admiten tasas manuales.
- Ingresos/gastos históricos conservan su referencia; posiciones actuales usan valoración actual identificada. Correcciones y cambio de moneda base no alteran importes originales silenciosamente.
- Sin tasa válida: mostrar originales y consolidado incompleto. Nunca asumir 1:1. La valoración cambiaria no es ingreso/gasto operativo ni garantiza liquidez en otra moneda.

## 7. Persistencia y consistencia

### 7.1. Modelo relacional y SQL

- Base compartida con aislamiento explícito por espacio; no una base o esquema por espacio.
- Identificador de espacio en entidades correspondientes y claves/relaciones que impidan vínculos entre espacios.
- Restricciones de unicidad para membresías, revisiones vigentes, ocurrencias, acciones idempotentes e identificadores externos.
- Restricciones simples de estado, valores y referencias; reglas entre sumas de filas validadas bajo transacción y bloqueos, no mediante supuestos `CHECK` entre filas.
- SQL parametrizado y generado por sqlc con `pgx/v5`; ordenaciones dinámicas mediante lista permitida.
- Autorización en la consulta, antes de paginar o agregar; no cargar todo y filtrar después en Go.
- JSONB solo para metadatos acotados, no para esconder el modelo financiero. Sin reglas de negocio ocultas en triggers o procedimientos como base inicial.
- RLS no es requisito inicial ni sustituto de autorización y pruebas; incorporarla exigiría diseño explícito de políticas y contexto del pool.
- Dimensionar `pgxpool` considerando la suma de procesos; límites, esperas y tiempos máximos documentados.

### 7.2. Transacciones y concurrencia

Una escritura financiera confirma operación, revisión, efectos, saldos derivados, aplicaciones, cumplimiento, auditoría y trabajo pendiente asociado dentro de una transacción. El éxito se informa después del `COMMIT`.

- `READ COMMITTED` como base; bloqueos explícitos sobre recursos que compiten. `SERIALIZABLE` solo con justificación e implementación de sus reintentos.
- Bloquear cuentas y recursos en orden determinista. Para aplicar pagos, bloquear movimiento y obligaciones antes de validar capacidad restante.
- Coordinar cambios de permisos/membresía con escrituras sensibles para que una autorización previa no permita confirmar después de su revocación.
- Versiones esperadas en modificaciones para rechazar sobrescrituras concurrentes y permitir revisar el estado vigente.
- Serializar admisiones y consumos que compitan por un límite. Un mutex Go no protege frente a otro proceso.
- Reintentar transacciones completas solo ante errores recuperables, con límite, espera y cancelación; nunca repetir efectos externos dentro del reintento.
- Las lecturas compuestas que deban explicar una misma cifra usan un snapshot coherente, por ejemplo una transacción de lectura `REPEATABLE READ` acotada.

### 7.3. Idempotencia

Los comandos sensibles reciben una identidad estable de acción. El registro relaciona actor, espacio, tipo de acción, clave, huella del contenido, estado y referencia al resultado, con exclusión transaccional de ejecuciones concurrentes.

- Misma clave y contenido: recuperar resultado sin duplicar efectos. Contenido distinto: conflicto.
- Una respuesta perdida después del commit no convierte la operación en fallida; el reintento recupera su resultado.
- Antes de devolver un resultado antiguo se comprueba autorización actual; no reutilizar respuestas sensibles sin revisión.
- Claves diferentes no prueban duplicidad: similitudes se presentan para revisión, no se eliminan automáticamente.
- Aplicar también a invitaciones, lotes, confirmaciones del asistente y cambios de contratación.
- Definir retención de verificadores y reconocimiento de reintentos tardíos antes de eliminarlos; no prometer idempotencia indefinida con claves ya purgadas.

### 7.4. Migraciones

Goose ejecuta migraciones como tarea explícita y controlada, no desde cada instancia al iniciar. Migraciones publicadas inmutables, cambios compatibles antes de retirar estructuras, revisión de operaciones destructivas y corrección hacia adelante como estrategia habitual. Probar tanto base vacía como actualización desde versiones soportadas; el código sqlc se regenera y verifica, no se modifica manualmente.

## 8. Identidad y sesiones propias

El módulo propio gestiona registro, verificación, credenciales, recuperación, MFA y sesiones. El correo solo entrega mensajes; no toma decisiones de identidad. No se construye un servidor OAuth/OIDC ni un sistema genérico de identidad.

### 8.1. Contraseñas y recuperación

- Argon2id con salt aleatorio por contraseña, parámetros y versión almacenados y comparación segura. Calibrar costo y concurrencia; no bajar del mínimo de referencia de OWASP sin revisión de seguridad.
- Admitir frases largas, sin truncamiento ni composición arbitraria; bloquear contraseñas comunes o comprometidas y no exigir rotación periódica sin motivo.
- Tokens de verificación, recuperación y cambio de correo aleatorios, con hash almacenado, propósito único, caducidad, consumo atómico e invalidación al reemplazarse.
- Respuestas públicas y tratamiento temporal que reduzcan enumeración de usuarios. Límites de intentos por identidad y origen, reenvío controlado y protección contra bloqueo malicioso de terceros.
- Reautenticación para cambios sensibles, aviso de seguridad y revocación de sesiones correspondiente. Recuperar contraseña no desactiva MFA.
- No registrar contraseñas, tokens, códigos ni enlaces utilizables. No enviar secretos al asistente.

### 8.2. Sesiones

Sesiones opacas en PostgreSQL, independientes por dispositivo; no JWT autocontenidos como mecanismo inicial.

- Tokens criptográficamente aleatorios; almacenar un verificador mediante hash, no el token utilizable. Para estos secretos aleatorios basta un hash rápido seguro; no Argon2id.
- Android transmite el token en `Authorization: Bearer`, únicamente por HTTPS, y lo conserva mediante almacenamiento protegido. No se acepta como parámetro de URL.
- Comprobar vigencia, estado del usuario y revocación en cada solicitud. Caducidad por inactividad y absoluta, rotación tras reautenticación o cambios sensibles y revocación individual o global.
- Acotar escrituras de última actividad sin omitir validación de acceso. Un identificador de dispositivo no constituye por sí solo prueba de identidad.
- Si una interfaz administrativa utiliza cookies: `Secure`, `HttpOnly`, alcance restringido, `SameSite` y protección CSRF; no reutilizar credenciales de cliente.
- TTL, umbrales, longitudes y concurrencia forman parte de configuración validada y probada. No admitir valores ilimitados implícitos ni inventar su dimensionamiento sin medición.

### 8.3. MFA

- TOTP disponible para clientes y obligatorio para personal interno. Activar solo tras comprobar un código.
- Secreto cifrado con cifrado autenticado y clave separada de la base; no puede almacenarse únicamente como hash porque debe verificarse TOTP.
- Ventana temporal acotada, intentos limitados y prevención atómica de reutilización de códigos aceptados.
- Códigos de recuperación aleatorios, con hash y consumo único. Cambiar factores exige reautenticación y notificación.
- No omitir un factor configurado por disponer del correo o de una sesión antigua. La biometría local no se presenta como MFA del servidor por sí sola.
- Si se pierden factores y recuperación, no existe desactivación ordinaria por soporte. Recuperación excepcional requiere procedimiento de verificación y dos aprobadores distintos; sin evidencia suficiente se deniega.

## 9. Espacios y autorización

### 9.1. Contexto y permisos

El permiso efectivo exige identidad vigente, membresía vigente, facultad del espacio, permiso sobre los datos y capacidad aplicable. Ninguno sustituye a los demás.

- Un usuario pertenece a varios espacios; al registrarse obtiene su espacio personal mediante un flujo idempotente sin altas parciales.
- Toda acción financiera identifica el espacio explícitamente. Cambiar el selector no cambia el destino de una operación iniciada ni existe un espacio activo global impuesto a todos los dispositivos.
- Propietario/administrador no equivale a lector de cuentas privadas. Un lector nunca obtiene operación por una excepción de cuenta.
- Consultar y operar no conceden administración de accesos, responsabilidad ni eliminación.
- Ser responsable no acredita titularidad bancaria. Registrar una contraparte no crea un usuario.
- La visibilidad propuesta para cuentas nuevas es explícita y respeta la preferencia del espacio; invitar al primer integrante no comparte cuentas existentes.
- Crear transferencias exige operar ambos extremos del mismo espacio; corregir efectos exige permiso sobre todas las posiciones afectadas.
- Consultar una relación muestra solo el extremo autorizado, sin filtrar nombres, documentos o referencias restringidas. No se simulan transferencias internas entre espacios.
- Los agregados utilizan datos autorizados y expresan su alcance, sin revelar número, nombres o importes de cuentas privadas excluidas.

La contratación conserva la unidad por espacio y separación definidas en `PRODUCT_DESIGN.md`, apartado 6.9. Capacidades y consumos se controlan de forma central y transaccional; no otorgan permisos financieros ni se trasladan entre espacios. Sus confirmaciones no crean movimientos personales. La responsabilidad de facturación es distinta de propiedad y membresía.

### 9.2. Invitaciones

Invitación por correo, rol y espacio identificados, aceptación explícita por el destinatario con ese correo verificado. Guardar estados, caducidad y verificador del enlace; reenviar invalida el anterior. Al aceptar se revalidan remitente, facultades, membresía no duplicada y capacidades vigentes. La incorporación es atómica e idempotente, no sustituye otras pertenencias y explica el acceso que conceden las cuentas compartidas con todo el espacio.

### 9.3. Revocación y artefactos derivados

Los permisos alcanzan búsquedas, reportes, archivos, exportaciones, notificaciones, proyecciones, auditoría visible al cliente y conversaciones. Se comprueban al ejecutar trabajos y al abrir resultados antiguos.

Registrar dependencias de recursos o alcance de los artefactos derivados. Si una revocación afecta una exportación, respuesta, resumen o conversación persistida, bloquear el contenido afectado hasta regenerarlo o reducirlo de forma segura. No basta autorizar únicamente las nuevas consultas de IA. La revocación no puede retirar información ya vista o descargada.

## 10. Salida y conservación restringida

La salida revoca la membresía, no elimina la identidad ni otras pertenencias. Se conservan autoría y hechos del espacio. El propietario resuelve la transferencia antes de salir de un espacio activo; la revocación por seguridad no se retrasa esperando cooperación y debe conservarse o restablecerse responsabilidad administrativa sin ampliar accesos financieros.

**Ninguna cuenta continúa operativa sin responsable vigente.** Esta precisión sustituye la exigencia absoluta de responsabilidad activa de toda cuenta compartida del apartado 6.7: una cuenta inmovilizada puede conservarse mientras se regulariza.

- Salida voluntaria: mostrar accesos que perderá y recursos afectados; permitir exportación previa solo del alcance autorizado y resolución explícita de responsabilidades.
- Las cuentas privadas bajo responsabilidad del saliente permanecen en el espacio, con saldo, moneda, historia y relaciones intactos; quedan inmovilizadas por desvinculación.
- Inmovilizar no liquida ni fuerza saldo cero. No se crean ajustes, transferencias, copias a otro espacio ni accesos para el antiguo integrante.
- No se exponen a propietarios, administradores o soporte por quedar sin responsable activo. Los efectos en cuentas relacionadas siguen visibles solo dentro del alcance permitido.
- Cuentas compartidas: reasignar a un integrante vigente autorizado; si no puede completarse, inmovilizar para nuevas operaciones, manteniendo lectura autorizada e historia.
- Planificación y documentos exclusivamente privados siguen conservación restringida; los compartidos requieren responsabilidad vigente para operar.
- La nueva invitación no reactiva recursos ni permisos anteriores automáticamente. Rehabilitar requiere acción explícita y validar identidad, responsabilidad y permisos actuales.
- Pueden existir recursos conservados sin lector financiero autorizado. Se mantienen según el ciclo de vida del espacio, sin conservar credenciales innecesarias ni abrir una vía de acceso residual.

## 11. Deudas, cobros y tarjetas

### 11.1. Acuerdos y aplicaciones

- Separar acuerdo, posición, calendario, pago real y aplicación. Ampliar una deuda simple conserva identidad e historia.
- Capital original, capital pendiente, saldo total, costos y datos desconocidos son conceptos distintos. No reconstruir contratos ni imputaciones bancarias sin evidencia.
- Pagos parciales, varias cuotas, abonos extraordinarios y excedentes se registran explícitamente. Aplicaciones sumadas no exceden el importe disponible del movimiento.
- Pago sin desglose conserva el total conocido y distribución pendiente; no atribuye todo a capital ni publica amortización exacta.
- Costos ya reconocidos no se reconocen otra vez al pagar. Condonación, pérdida, refinanciamiento y compensación son hechos explícitos, sin efectivo ficticio ni neteo automático de posiciones de una contraparte.
- Atraso y cumplimiento se derivan de vencimientos y aplicaciones vigentes; anular o desvincular un pago los recalcula.

### 11.2. Servicios pendientes de cobro

Se concreta el apartado 13 del producto con una sola regla, sin sistemas contables configurables por usuario:

| Situación | Resultado |
| --- | --- |
| Servicio previsto, cobro hipotético o importe estimado | Expectativa; no crea ingreso ni derecho efectivo. |
| Servicio ya prestado, importe confirmado y derecho reconocido expresamente por el usuario | Reconoce ingreso y derecho de cobro una vez. |
| Cobro posterior de ese derecho | Aumenta efectivo y reduce derecho; no reconoce otro ingreso. |
| Servicio y cobro registrados juntos | Una operación reconoce ingreso y efectivo; no deja pendiente ficticio. |

Un derecho de S/ 1.000 con cobro posterior de S/ 400 deja S/ 600 por cobrar y no S/ 1.400 de ingreso. Derechos incluidos en la referencia inicial no producen ingreso actual al incorporarse. Antecedentes desconocidos se identifican, no justifican duplicar ingreso. Condonar un derecho válido registra pérdida; corregir un derecho erróneo es una corrección distinta.

### 11.3. Tarjetas

Agrupación de posiciones separadas por moneda. Deuda actual, saldo a favor, pendiente del estado, mínimo y crédito disponible no son intercambiables. Estados confirmados y reconstrucciones estimadas se distinguen. Las cuotas distribuyen exigibilidad futura, no repiten el gasto original; pagos y devoluciones se aplican sin inventar distribución bancaria. Saldo a favor y crédito disponible no se suman al efectivo.

## 12. Planificación, visibilidad y calendario

### 12.1. Recursos sin cuenta

- Documentos, compromisos e ingresos esperados sin cuenta: privados por defecto para su creador, con posibilidad de compartir explícitamente con integrantes o todo el espacio.
- Separar consulta y administración; pertenecer al espacio o administrarlo no concede lectura automática.
- Al relacionar contenido restringido, el acceso efectivo satisface tanto la configuración del recurso como **todas** sus relaciones sensibles. Agregar una relación no amplía permisos y muestra antes de confirmar la reducción de acceso que ocasionará.
- Un vencimiento sin cuenta puede figurar en la agenda autorizada, identificado como no asignado; no se atribuye a una cuenta para simular saldo proyectado.
- Presupuestos compartidos con facultad de edición propia; consumo solo del alcance autorizado. Compartir planificación no comparte los movimientos privados relacionados.
- Reservas con fondos identificados. Una intención sin asignación no reduce disponibilidad concreta; relacionar cobertura con compromisos evita descontarlos dos veces.

### 12.2. Series y cumplimiento

Series manuales versionadas, con importe fijo, estimado, rango o desconocido. Materializar ocurrencias por horizonte acotado, con identidad única e idempotencia, y recuperar la generación tras interrupciones. La agenda consulta vencimientos de su origen, no duplica cuotas o tarjetas como series independientes.

Llegar a una fecha solo permite preparar ocurrencias y recordatorios. Registrar o vincular un movimiento y confirmar su aplicación es lo que actualiza cumplimiento. Una obligación confirmada solo se completa al cubrir su importe; una estimada requiere confirmación del monto real, no tolerancia automática por proximidad. Declaraciones sin movimiento son incompletas dentro del seguimiento.

Pausar, cancelar, cambiar condiciones u omitir una ocurrencia conserva historia y obligaciones existentes. Posponer un recordatorio no renegocia vencimiento ni cancelar seguimiento cancela contratos externos. Equivalencias mensuales/anuales muestran la regla de cálculo y son estimaciones comparativas, no gasto real.

### 12.3. Fechas y reglas cerradas

| Aspecto | Regla |
| --- | --- |
| Fecha financiera o vencimiento | Fecha civil (`DATE`), distinta del instante técnico. |
| Creación, corrección y auditoría | Instantes (`TIMESTAMPTZ`). |
| Zona horaria | Identificador IANA; base de zonas disponible durante ejecución. |
| Períodos | Inicio incluido, fin excluido; límites desde el ancla original, sin huecos ni solapamientos. |
| Días 29, 30 o 31 inexistentes | Último día disponible, conservando día nominal para meses posteriores. |
| Dos fechas mensuales | Días nominales distintos; si coinciden al recortarse, conservar dos ocurrencias independientes y mostrarlo en la vista previa. |
| Recurrencia anual del 29 de febrero | 28 de febrero en años no bisiestos; vuelve al 29 cuando existe. |
| Hora de recordatorio inexistente | Primer instante válido posterior; no cambia fecha financiera. |
| Hora repetida | Primera ocurrencia; identidad del recordatorio evita doble envío. |
| Cambio de zona, frecuencia o ancla | Vista previa, condiciones versionadas y ninguna reescritura de historia confirmada. |

Cada dos semanas no equivale a dos veces al mes. Los cálculos usan el calendario y ancla, no sumas ciegas de horas o normalizaciones que desplacen el día elegido.

### 12.4. Presupuestos y proyección

Presupuesto por categoría principal y período operativo, consumido por gasto neto vigente: compras con tarjeta consumen al ocurrir; pagos y capital no repiten consumo; devoluciones se aplican en su fecha; excepcionales incluidos por defecto.

Proyección determinista de 7, 30 y 90 días, por cuenta y moneda cuando corresponda. Separar confirmado, estimado, incierto y desconocido; no usar cobros inciertos para ocultar faltantes en la lectura conservadora. Cada salida se cuenta una vez, incluida la relación cargo con tarjeta/pago posterior y reserva/cobertura. Mostrar supuestos, exclusiones y datos faltantes; no recomendar gasto seguro con evidencia insuficiente ni modificar saldos.

## 13. Clasificación, reportes y calidad de datos

- Categorías de ingreso/gasto con máximo dos niveles; identidad independiente de traducción. Archivar, fusionar o reclasificar conserva referencias, trazabilidad y explicación del efecto en historia y presupuestos.
- Comercio, contraparte y etiquetas pertenecen al espacio. Consolidar duplicados no pierde relaciones ni divulga nombres entre espacios; no se exige catálogo global.
- La falta de categoría no bloquea un hecho conocido. Transferencias y capital no requieren categoría de consumo; intereses y comisiones sí se clasifican.
- Una misma consulta semántica sirve a reportes, detalle, exportaciones e IA: mismos filtros, permisos, período, moneda y referencias. SQL compartido o casos de consulta comunes evitan fórmulas paralelas.
- Agregados y detalle coinciden; comparaciones parciales usan tramos equivalentes. Base cero no produce porcentaje inventado; historial insuficiente se informa.
- Reportes incluyen alcance autorizado, actualización, fuentes y exclusiones. La evolución separa referencia inicial, flujos, ajustes y valoración cambiaria. Las cuentas archivadas/inmovilizadas conservan historia autorizada.
- Calidad de datos identifica origen y corrección de: clasificación faltante, duplicados posibles, pagos sin desglose/aplicación, tasas faltantes, ajustes no explicados, reconciliaciones antiguas, historia parcial, vencimientos no confirmados y declaraciones incompletas.

## 14. Documentos e incorporación

### 14.1. Archivos

Metadatos y relaciones en PostgreSQL; contenido fuera de las filas financieras, mediante un adaptador de archivos privados. Identificadores generados por servidor, validación de tamaño/tipo/integridad/cuota, cuarentena y análisis de seguridad antes de disponibilidad. No ejecutar archivos ni renderizar contenido activo sin control.

Descarga autenticada por backend con autorización actual, no enlaces reutilizables que contradigan la revocación. Proteger nombres, rutas y cabeceras. Subir no modifica saldos ni autoriza análisis de IA. Los documentos independientes siguen la sección 12.1 y tienen listado/búsqueda por sus metadatos autorizados.

### 14.2. Importación

Flujo: carga → mapeo → staging/validación → vista previa → revisión → confirmación → aplicación → resultado consultable.

- Interpretación explícita de fechas, separadores, monedas, signos y cuenta/espacio.
- Errores por fila y posibles duplicados sin omisiones ni eliminación automática por similitud.
- Lotes con límites documentados y aplicación atómica por lote confirmado. No informar éxito completo si existiera una incorporación parcial.
- Revalidar permisos y datos al confirmar; usar los casos de uso financieros y distinguir antecedentes anteriores al seguimiento.
- Conservar vínculo a lote, filas/identidades incorporadas, estado y resultado para reintentos y recuperación después del commit.
- Reversión con análisis de relaciones y cambios posteriores; correcciones controladas, no borrado masivo de filas.

### 14.3. Exportación

Snapshot coherente, filtros y permisos iguales al reporte, contexto de moneda/conversión y generación con memoria acotada. Verificar acceso al generar y descargar; proteger CSV contra interpretación de fórmulas en hojas de cálculo. Expiración y purga conforme a sección 17, sin referencias utilizables como acceso permanente.

## 15. API y asistente

### 15.1. Contrato HTTP

- REST sobre HTTPS, versión inicial `/v1`, espacio explícito en rutas financieras y OpenAPI generado/revisado.
- Huma valida estructura; casos de uso validan negocio y autorización. No habilitar parches automáticos genéricos sobre operaciones financieras.
- Errores seguros con código estable, campos afectados e identificador de solicitud, compatibles con [Problem Details, RFC 9457](https://www.rfc-editor.org/rfc/rfc9457). No reflejar secretos o valores sensibles en errores de validación.
- Conflictos de versión/idempotencia distinguibles de validación y fallos técnicos. Conservar información suficiente para que el cliente no pierda la captura.
- Paginación por cursor y orden estable; filtros y tamaños limitados. Un cursor no concede permisos ni promete snapshot indefinido entre páginas.
- Trabajos con estado y resultado consultables; `202 Accepted` no significa operación financiera terminada. Las operaciones simples confirman efectos antes de responder éxito.
- Compatibilidad con clientes Android soportados, sin definiciones financieras locales ni sincronización offline. Los contratos son del producto, no una API pública para terceros.

### 15.2. Asistente

Integración acotada, sin framework de agentes, SQL generado por el modelo, acceso directo a PostgreSQL, herramientas generales de escritura ni cálculos financieros delegados al modelo.

- Herramientas de consulta tipadas llaman los mismos casos autorizados que reportes. Respuestas con período, moneda, alcance, evidencia y advertencias.
- Las cifras principales proceden de resultados estructurados; el texto del modelo no se acepta como fuente financiera. Distinguir hechos, interpretación y alternativas.
- Solicitar datos esenciales faltantes sin adivinar cuenta, moneda o contraparte. Sin recomendaciones de productos financieros concretos ni simulaciones fuera de alcance.
- Persistir propuesta con usuario, espacio, acción tipada, contenido, efectos, versiones relevantes, caducidad e identidad estable.
- Confirmación explícita por una acción del producto, no por un mensaje del modelo. Revalidar identidad, permisos, capacidad y versiones; ejecutar el caso de uso normal y consumir propuesta una vez. Cambios relevantes obligan a revisar de nuevo.
- Conversaciones privadas por usuario/espacio y sometidas a revocación de derivados de la sección 9.3. Mensajes antiguos no acreditan el estado actual.
- Consentimiento informado, minimización, referencias reducidas y sin credenciales ni números completos. Los mensajes pueden contener datos personales: no prometer anonimato absoluto.
- Archivos y descripciones son datos no confiables, no instrucciones. Sin lectura documental, búsqueda semántica ni RAG en el alcance definido.
- Acotar llamadas, contexto, duración, concurrencia y consumo. Fallo o desactivación del asistente no afecta operaciones financieras normales.

## 16. Trabajos durables y notificaciones

Cola acotada en PostgreSQL y outbox transaccional: confirmar el hecho y la intención asociada juntos; procesar después. Casos: correo, recordatorios, importaciones confirmadas, exportaciones, limpieza y tareas de conciliación.

- Procesamiento al menos una vez, consumidores idempotentes y claves de deduplicación cuando proceda.
- Arrendamiento temporal de trabajos, recuperación de abandonados, reintentos con espera y límite, fallo visible y reintento operativo auditado.
- Concurrencia acotada y separación de cargas; trabajos pesados no bloquean recordatorios.
- No mantener transacciones esperando correo o IA. No confiar en goroutines no persistidas después de responder.
- Antes de ejecutar o entregar contenido, revalidar estado, permisos, preferencias y vigencia del destinatario. Notificaciones discretas, sin importes sensibles por defecto, y apertura con acceso actual al espacio correcto.
- Un recordatorio no paga, omitir no paga y un envío fallido no revierte un hecho financiero confirmado.
- No prometer exactamente una vez entre base y servicios externos. Ante resultado externo incierto sin idempotencia, conservar incertidumbre y conciliar, no repetir ciegamente.
- Cierre ordenado: dejar de aceptar trabajo, cancelar con plazos, terminar o liberar tareas de forma recuperable y cerrar recursos.

## 17. Administración, retención y eliminación

### 17.1. Personal y soporte

Identidades, credenciales y sesiones internas independientes de clientes, aunque se reutilicen primitivas de seguridad. Sin alta pública; MFA obligatorio; ningún token de cliente se acepta como interno. Separar soporte, administración sensible y facultades de facturación, sin acceso financiero implícito ni suplantación del usuario.

Acceso a contenido financiero por soporte requiere caso, motivo, autorización de quien puede concederla, recursos y acciones concretos, duración máxima de **60 minutos**, revocación inmediata y nueva autorización para extender. El propietario no autoriza lectura de cuentas privadas ajenas por ser propietario. Consulta no permite exportar ni modificar; las correcciones financieras se confirman por el cliente mediante el flujo normal. Acciones administrativas sensibles y recuperaciones excepcionales requieren dos aprobadores distintos.

Auditar operador real, ámbito, motivo, tiempo, consultas sensibles, exportaciones y cambios. No incluir copias innecesarias de documentos o conversaciones. Verificar la concesión en cada acceso, no solo al iniciar sesión.

### 17.2. Plazos base

| Datos | Conservación o purga |
| --- | --- |
| Operaciones, revisiones y documentos vigentes | Vida del espacio, también en cuentas archivadas/inmovilizadas; eliminación según el proceso correspondiente. |
| Conversaciones | Hasta eliminación por su usuario o del contexto correspondiente; inaccesibles al revocar el acceso que las sustenta. |
| Exportaciones generadas | Expiración y eliminación en un máximo de 24 horas. |
| Archivos temporales de importación | Hasta 7 días desde finalización o abandono; conservar procedencia necesaria sin exigir el archivo original completo. |
| Logs técnicos | 30 días, sin contenido financiero por defecto. |
| Auditoría de seguridad y accesos de soporte | 12 meses. |
| Datos eliminables de solicitudes aprobadas | Purga en un máximo de 30 días. |
| Copias de respaldo | Hasta 35 días adicionales desde la purga; hasta 65 días desde la solicitud aprobada. |

La auditoría financiera sigue la vida del hecho que explica, no el plazo de logs de seguridad. Estos son valores de diseño, no una afirmación de obligaciones legales universales. Toda excepción exige fundamento, alcance mínimo y revisión o finalización explícita; no retención indefinida preventiva.

### 17.3. Eliminación y restauración

- Usuario: reautenticación/confirmación, resolución de propiedad y responsabilidades, revocación de sesiones/tokens/trabajos, eliminación de credenciales, factores, preferencias y conversaciones personales; conservar hechos compartidos y minimizar o seudonimizar autoría según corresponda.
- Espacio: facultad y confirmación expresa, inhabilitación para nuevas operaciones, revocación de acceso y trabajos, purga coordinada de filas y archivos, con excepciones de conservación justificadas. Resolver obligaciones asociadas sin dejar referencias activas huérfanas.
- Eliminar una identidad no borra operaciones de terceros ni expone cuentas privadas desvinculadas; estas conservan el ciclo de vida restringido del espacio.
- No utilizar un `DELETE CASCADE` indiscriminado ni afirmar purga instantánea de respaldos.
- Mantener un registro mínimo de eliminaciones durante el horizonte de restauración y reaplicarlo antes de habilitar accesos o trabajos. Conciliar efectos externos antes de reactivar procesos para evitar duplicaciones.
- Respaldos protegidos y pruebas de restauración que comprueben integridad, permisos y relaciones entre archivos y metadatos.

## 18. Seguridad, errores y observabilidad

- HTTPS, conexiones a datos protegidas, privilegios mínimos y credenciales de migración distintas de las de aplicación.
- Configuración validada al iniciar; secretos fuera del código, separados por entorno y nunca incluidos en logs o errores públicos.
- Límites de cuerpo, archivos, filas, filtros, páginas, procesos y uso del asistente. Control de abuso efectivo entre procesos, no solo contadores en memoria.
- `context.Context` y tiempos máximos en SQL y llamadas externas; cancelación propagada. Goroutines con propietario, límite y condición de salida.
- Entradas parametrizadas/permitidas; defensa contra SQL injection, rutas arbitrarias y contenido activo. No recoger números completos de cuentas o tarjetas para seguimiento manual.
- Errores de negocio identificables y mensajes públicos seguros. Enriquecer técnicos con `oops`, mantener compatibilidad con `errors.Is/As` y registrar una vez en la frontera responsable.
- Separar historia financiera, auditoría administrativa y logs operativos. Roles de ejecución sin modificación arbitraria de auditoría y accesos privilegiados controlados; no llamarla inalterable si un administrador puede reescribirla.
- `slog` JSON con identificadores de solicitud/traza, operación, resultado y duración. Excluir cuerpos, tokens, códigos secretos, descripciones, conversaciones e importes por defecto; también parámetros SQL sensibles y atributos de trazas.
- Métricas de latencia/error, saturación/espera del pool, consultas/bloqueos/reintentos, discrepancias financieras, edad/fallos de trabajos, importaciones y uso/fallos del asistente.
- OpenTelemetry en fronteras relevantes con contexto y muestreo. Etiquetas de cardinalidad acotada: no IDs de usuario/espacio ni URLs completas.
- Diagnóstico y profiling restringidos; instrumentación sin una plataforma de observabilidad propia obligatoria.

## 19. Rendimiento y verificación

### 19.1. Medición

Índices a partir de consultas reales, revisión de planes de ejecución, sin N+1, paginación, memoria y concurrencia acotadas. No incorporar cachés, particionado o infraestructura adicional sin evidencia. Las proyecciones de saldo se actualizan transaccionalmente; no se recalcula todo el historial en cada lectura cotidiana.

Objetivos iniciales para validar, no resultados garantizados: p95 de lecturas cotidianas menor de 300 ms, escrituras simples menor de 500 ms y reportes interactivos habituales menor de 2 s, medidos como procesamiento servidor. Especificar carga, volumen, recursos, errores y metodología; medir aparte red móvil, arranque y llamadas externas. Exportaciones grandes son asíncronas.

Comparar benchmarks repetidos en condiciones y toolchain equivalentes, revisar asignaciones y perfiles y justificar optimizaciones con resultados. Una ejecución aislada o un porcentaje de cobertura no demuestra calidad.

### 19.2. Estrategia de pruebas

| Nivel | Verificación |
| --- | --- |
| Unitarias deterministas | Dinero, rango, redondeo, efectos, incertidumbre, calendario, proyección y reconocimiento. |
| Integración PostgreSQL | SQL, restricciones, transacciones, revisiones, saldos derivados, migraciones y consultas autorizadas. |
| Concurrencia de base | Sobreaplicación de pagos, idempotencia, admisiones/límites, revocaciones y ediciones simultáneas. |
| HTTP y contrato | Validación, errores, permisos, versiones, paginación y OpenAPI. |
| Fuzzing | Importes, fechas, CSV y entradas malformadas. |
| Recuperación | Interrupción antes/después del commit, arrendamientos, reintentos, importaciones y restauración. |
| Privacidad y seguridad | Cuentas privadas, agregados, documentos, sesiones, MFA, soporte y artefactos antiguos de IA. |
| Rendimiento | Benchmarks y carga reproducibles de rutas representativas. |

Tests independientes y aislados; PostgreSQL real mediante Testcontainers con la versión base, no SQLite como sustituto. Separar integración con etiquetas de compilación y ejecutarla obligatoriamente en validación. `go test -race` detecta carreras en memoria, no competencia transaccional en PostgreSQL.

Todos los casos del apartado 24 de `PRODUCT_DESIGN.md` deben corresponder a pruebas verificables. Añadir al menos: inmovilización sin exposición, revocación sin cooperación, reincorporación sin permisos automáticos, servicio reconocido sin doble ingreso, documento sin cuenta privado, dos fechas coincidentes, recuperación sin salto de MFA, soporte vencido y restauración sin resurrección de datos eliminados.

### 19.3. Método de desarrollo

Desarrollar capacidades verticales: contrato → reglas → autorización/persistencia → pruebas → observabilidad → documentación. No crear primero todas las tablas ni capas vacías.

La validación automatizada incluye formato, análisis estático, tests, race detector, integración, sqlc reproducible, revisión de OpenAPI, migraciones, verificación de módulos y vulnerabilidades mediante `govulncheck`, además de revisión del artefacto construido. Fijar versiones de las herramientas al configurarlas. No usar datos financieros reales en desarrollo.

Una funcionalidad está terminada cuando contempla permisos, incompletitud, reintento, concurrencia, revocación, interrupción, trazabilidad y diagnóstico, además del camino exitoso. Cambios de comportamiento actualizan contrato, pruebas y documentos; decisiones relevantes incluyen motivo y consecuencias, sin documentación ceremonial.
