# Plan de desarrollo por bloques

**Estado:** bloques 1–3 cerrados y verificados; bloques 4–14 pendientes.  
**Referencias:** [producto](PRODUCT_DESIGN.md), [diseño técnico](TECHNICAL_DESIGN.md) e [instrucciones del proyecto](../../AGENTS.md).

## 1. Propósito y alcance

Este plan define **qué se construye en cada bloque, en qué orden y qué debe estar listo para avanzar**. El comportamiento completo, las reglas, dependencias, versiones y decisiones se consultan en `PRODUCT_DESIGN.md` y `TECHNICAL_DESIGN.md`; el resumen de un bloque no reduce ese alcance ni sustituye las especificaciones.

Este plan organiza exclusivamente el backend central: el cerebro del finance tracker. Cada bloque entrega reglas, persistencia, contratos API y garantías verificadas para que los equipos de clientes puedan integrarse. Otro equipo desarrollará el primer cliente, la aplicación Android, en un proyecto separado; su implementación no forma parte de las tareas ni de los criterios de cierre de este plan. Las capacidades administrativas se entregan del lado del servidor, en el ámbito de acceso separado previsto por el diseño. Exponer endpoints sin implementar y probar su comportamiento completo no equivale a terminar el backend.

El alcance del servidor definido por ambos diseños se distribuye entre los bloques. Las experiencias de cliente sirven como referencia para los contratos y casos de uso que el backend debe soportar, no como tareas de desarrollo de interfaces. Las posibilidades de ampliación del apartado 23.2 del producto no se convierten en tareas obligatorias. No se fija un calendario ni una duración por bloque.

### Diseño evolutivo y comunicación

El diseño formal recoge lo que el equipo entendió y acordó al aterrizar el producto; **no está escrito en piedra**. Si durante el desarrollo aparece una omisión, un error o una solución más simple, segura o eficiente:

1. Comunicar el hallazgo antes de modificar la decisión o el comportamiento afectado.
2. Explicar el sustento, la alternativa propuesta y las consecuencias sobre alcance, datos, permisos, historia, compatibilidad y orden de trabajo.
3. Acordar el cambio con el equipo y actualizar los diseños y este plan cuando corresponda.
4. Implementar junto con migraciones y pruebas necesarias.

No continuar con una solución incorrecta solo porque figura en el diseño ni introducir cambios silenciosos. Si falta una decisión indispensable para un bloque, resolverla y comunicarla antes de comprometer su implementación o cierre.

## 2. Reglas de ejecución y cierre

- Los bloques se ejecutan en orden. **El bloque N comienza con todos los anteriores cerrados y verificados.** Los prerrequisitos destacados no sustituyen esta regla acumulativa.
- El equipo concentra su trabajo en un bloque. Puede repartir tareas independientes dentro de él, respetando el orden interno y las responsabilidades de módulos; no abrir bloques posteriores para compensar un cierre incompleto.
- Antes de empezar, revisar las secciones de ambos diseños referenciadas y confirmar que las dependencias están disponibles. No sustituir una dependencia pendiente por un atajo que omita reglas.
- Cada capacidad del servidor se entrega verticalmente: contrato, reglas, persistencia, autorización, pruebas, observabilidad y documentación. No desarrollar todas las tablas o capas primero.
- Seguridad, idempotencia, concurrencia, errores, auditoría, límites y retención se aplican desde el bloque que introduce el recurso, no al final.
- Los recursos nuevos se integran en permisos, salida de integrantes, consultas y ciclo de vida ya existentes. Esta extensión prevista no significa reabrir bloques cerrados: cada bloque mantiene la regresión del conjunto.
- No crear por adelantado todos los módulos, esquemas, adaptadores o dependencias opcionales. El bloque 1 prepara las herramientas; los módulos se incorporan cuando se usan.
- Los dobles de prueba son válidos para pruebas aisladas, no para declarar terminada una integración real. Los estados parciales, decisiones faltantes o bloqueos quedan visibles.

### Puerta común de cierre

Para marcar un bloque como terminado deben existir:

1. Entrega funcional demostrable y criterios particulares de cierre cumplidos.
2. Pruebas del bloque y regresión de lo construido, incluyendo casos de error, permisos, reintento y concurrencia aplicables.
3. Migraciones, generación sqlc/OpenAPI y validaciones automatizadas correctas; sin cambios manuales de código generado.
4. Recorrido del caso de uso mediante la API y evidencia de resultado real del servidor, no solo respuestas simuladas; contratos documentados y listos para integración por los equipos de clientes.
5. Diagnóstico y documentación actualizados, sin dependencias esenciales aplazadas a un bloque futuro.

El bloque 1 se valida como preparación, sin exigir funcionalidades de negocio. El bloque 2 valida la ejecución técnica antes de la identidad de usuarios. El último bloque audita la aceptación integral; no sustituye las puertas anteriores.

## 3. Secuencia de trabajo

Todos los bloques parten como **pendientes**. Se marcan únicamente cuando superan la puerta común y su cierre particular.

| Estado | Bloque | Entrega principal |
| --- | --- | --- |
| [x] | 1. Preparación del entorno y del proyecto | Inicio reproducible del desarrollo. |
| [x] | 2. Base de ejecución y garantías compartidas | HTTP, persistencia, trabajos y fundamentos probados. |
| [x] | 3. Identidad, espacios y control de acceso | Usuarios reales y contexto autorizado multiespacio. |
| [ ] | 4. Cuentas y organización financiera | Posiciones, referencias iniciales, permisos y catálogos. |
| [ ] | 5. Operaciones y consistencia financiera | Registro cotidiano, correcciones y reconciliación. |
| [ ] | 6. Vencimientos, aplicaciones y recurrentes | Motor común de obligaciones y agenda. |
| [ ] | 7. Deudas, préstamos y derechos de cobro | Acuerdos y seguimiento completo de pagos y cobros. |
| [ ] | 8. Tarjetas y compras en cuotas | Ciclos, estados y exigibilidad sin duplicar gasto. |
| [ ] | 9. Documentos e incorporación de información | Archivos, incorporación guiada, importación y exportación de registros. |
| [ ] | 10. Presupuestos, reservas y proyecciones | Planificación sobre hechos y compromisos disponibles. |
| [ ] | 11. Reportes, calidad de datos e inicio | Lecturas completas y explicables del producto. |
| [ ] | 12. Asistente de inteligencia artificial | Consultas y propuestas con confirmación efectiva. |
| [ ] | 13. Administración y ciclo de vida completo | Gestión interna, contratación integrada, privacidad y eliminación. |
| [ ] | 14. Verificación integral y cierre | Aceptación completa y recuperación comprobada. |

La separación evita dos concentraciones de riesgo: construir todas las finanzas en un solo bloque y añadir seguridad o pruebas como una fase tardía. Los bloques agrupan capacidades completas, no tareas individuales o sprints.

## Bloque 1. Preparación del entorno y del proyecto

**Objetivo:** dejar al equipo preparado para comenzar código de negocio sin diferencias de entorno.

**Trabajo, en orden:**
1. Revisar el estado existente del proyecto, conservar trabajo ajeno y establecer control de versiones, convenciones y archivos básicos.
2. Preparar Go, Docker con contenedores Linux, PostgreSQL local y herramientas con las versiones del diseño técnico. Verificar compatibilidad y fijar los ejecutables, sin añadir dependencias opcionales.
3. Crear el módulo y esqueleto mínimo, configuración de ejemplo sin secretos, separación de configuración local/pruebas y comandos PowerShell para preparar, generar, ejecutar y validar.
4. Configurar Goose, sqlc, formato, análisis estático, pruebas y validación automatizada inicial. Preparar Testcontainers y datos sintéticos; no crear todo el esquema financiero.
5. Documentar la puesta en marcha del backend y cómo ejecutar sus validaciones.

**Cierre:** otro integrante reproduce desde una copia limpia la preparación, compilación y validaciones iniciales; PostgreSQL y contenedores de pruebas funcionan. No hay funcionalidad de negocio ni implementación anticipada de módulos.

**Referencias:** técnico 3–4 y 19; producto 4.

**Cierre verificado (29 de octubre de 2026):** preparación, compilación y validaciones completas reproducidas desde una copia limpia de los archivos versionados; PostgreSQL local y Testcontainers operativos. No se inició el bloque 2.

## Bloque 2. Base de ejecución y garantías compartidas

**Objetivo:** disponer de una base ejecutable para que las funcionalidades no tengan que inventar mecanismos técnicos propios.

**Prerrequisito clave:** entorno reproducible del bloque 1.

**Trabajo, en orden:**
1. Implementar arranque y cierre ordenado, configuración validada, conexión/pool PostgreSQL, ejecución explícita de migraciones e integración sqlc.
2. Montar `net/http` con Huma, contratos/errores comunes, límites, cancelación, logs, trazas, métricas y comprobaciones de estado, sin endpoints de negocio ficticios.
3. Implementar y probar transacciones, versión de edición e idempotencia como mecanismos reutilizables; distinguir confirmación persistida de respuesta HTTP perdida.
4. Construir auditoría técnica y ejecución durable de trabajos/outbox, reintentos, arrendamientos, consulta de estado y entrega de correo comprobable en el entorno de pruebas.
5. Preparar los tipos y reglas compartidas que necesitan los siguientes bloques: moneda/importe, precisión, fecha civil, zona horaria, anclas y períodos. La generación de recurrentes corresponde al bloque 6.

**Cierre:** una prueba de integración demuestra escritura y trabajo asociado atómicos, recuperación tras interrupción y ausencia de duplicación. HTTP, SQL y trabajos tienen tiempos y recursos acotados; no requieren identidad o recursos financieros futuros para funcionar técnicamente.

**Referencias:** técnico 3–8, 12.3, 15.1, 16 y 18–19; producto 3 y 22.2.

**Cierre verificado (29 de octubre de 2026):** `./scripts/dev.ps1 validate` completó formato, módulos, generación sqlc/OpenAPI sin deriva, validación de migraciones, vet/lint, pruebas unitarias, integración PostgreSQL/correo/HTTP real, detector de carreras en Linux, govulncheck y revisión/ejecución del artefacto. Se comprobaron concurrencia, rollback de escritura/auditoría/outbox, respuesta TCP perdida tras el commit, recuperación con arrendamiento vencido, resultados externos inciertos sin reenvío ciego, cupos independientes por cola, permisos del rol de ejecución y cierre ordenado. Los escenarios de integración también se ejecutaron de forma selectiva e independiente; dinero y fechas pasaron campañas breves de fuzzing. Una exportación limpia del índice de Git compiló y ejecutó el binario sin archivos locales ignorados.

Govulncheck detectó cero vulnerabilidades alcanzables y cero en paquetes importados; el aviso de módulo `GO-2026-5932` corresponde a `golang.org/x/crypto/openpgp`, que no se importa ni se utiliza. No se alteró la versión acordada de x/crypto por un paquete ajeno al alcance. No se inició el bloque 3 ni se registraron endpoints financieros ficticios.

## Bloque 3. Identidad, espacios y control de acceso

**Objetivo:** habilitar uso real del sistema con identidad propia, contexto multiespacio y autorización desde el comienzo.

**Prerrequisitos clave:** correo durable, persistencia, auditoría y calendario base del bloque 2.

**Trabajo, en orden:**
1. Implementar registro, verificación, acceso, recuperación y cambios de credenciales; sesiones por dispositivo, MFA y códigos de recuperación. Crear el espacio personal de forma idempotente al completar el alta.
2. Implementar espacios adicionales, configuración, membresías, roles y autorización del contexto explícito de cada solicitud mediante la API; la selección se conserva en el cliente. Identidad/sesión administrativa separada con MFA obligatorio, sin gestión interna financiera todavía.
3. Construir el control central de capacidades y consumos por espacio, estado efectivo y responsabilidad de facturación. Debe poder autorizar acciones con estado persistido y probado, no devolver siempre permitido. La gestión integral y confirmaciones externas se entregan en el bloque 13.
4. Implementar invitaciones y su revalidación al aceptar, transferencia de propiedad, retiro/salida y revocación. Preparar la comprobación de acceso a recursos y derivados que utilizarán los módulos siguientes.
5. Implementar preferencias personales y de notificación y verificar mediante la API los flujos de acceso, sesiones, espacios e invitaciones. Las solicitudes de datos personales se completan cuando existen todos los recursos, en el bloque 13.

**Cierre:** usuarios verificados operan en varios espacios con roles distintos; no hay acceso cruzado ni sesiones de cliente utilizables como internas. Repetir alta/invitación no duplica; retirar una membresía no afecta otras. La recuperación no elude MFA y ninguna acción omite el control de capacidades por estar en construcción.

**Referencias:** producto 6 y 22.1/22.4; técnico 8–10, 17.1 y 18.

**Cierre verificado (30 de octubre de 2026):** `./scripts/dev.ps1 validate` completó todas las puertas sobre el código final: formato, pruebas unitarias, vet/lint, módulos, generación sqlc/OpenAPI sin deriva, migraciones, integración PostgreSQL/HTTP/correo mediante SDK SES sobre HTTPS local, detector de carreras en Linux, govulncheck y compilación/ejecución del artefacto `0.3.0`. Las pruebas usan el rol de aplicación restringido y comprueban actualización desde la migración publicada del bloque 2 conservando recibos existentes, además de la instalación desde base vacía en la regresión de ejecución.

Se verificaron alta y espacio personal únicos bajo concurrencia, sesiones independientes y revocables, separación cliente/personal interno, MFA y códigos de recuperación de consumo único, recuperación sin omitir factores, cambios de credenciales, preferencias, múltiples espacios, roles, paginación, invitaciones revalidadas, cupos concurrentes, transferencia de propiedad sin traslado de facturación, retiro/reincorporación sin restaurar permisos anteriores y bloqueo de vistas previas antiguas tras la revocación. Una desconexión TCP después del commit recupera el resultado por la API sin duplicar el espacio. La sustitución del servicio conserva sesiones y correo pendiente; escenarios de reinicio/revocación/reintento también se ejecutaron aisladamente.

El perfil no comercial aprobado queda persistido por espacio con capacidades, consumo y vigencia finitos. Se probó su sustitución transaccional por un estado de origen comercial sin alterar membresías, datos ni responsabilidad de facturación; esto no simula cobros ni declara completada la contratación del bloque 13. La migración y las concesiones locales se aplicaron sin borrar datos; una exportación limpia del índice compiló y ejecutó el binario sin archivos locales ignorados. Govulncheck informó cero vulnerabilidades alcanzables y cero en paquetes importados; permanece únicamente el aviso de módulo OpenPGP ya documentado, que no se utiliza. No se inició el bloque 4 ni se implementó un cliente Android.

## Bloque 4. Cuentas y organización financiera

**Objetivo:** establecer dónde se registra el dinero y cómo se organiza, antes de introducir movimientos cotidianos.

**Prerrequisitos clave:** identidad, espacios, permisos y tipos monetarios disponibles.

**Trabajo, en orden:**
1. Implementar cuentas/posiciones de los tipos del producto, moneda, responsable, referencias iniciales fechadas, estado y calidad del dato. No duplicar deuda o derecho de cobro por su representación en cuentas.
2. Implementar permisos privados, de todo el espacio y de integrantes específicos; administración distinta de operación y lectura. Aplicar inmovilización y regularización al salir responsables.
3. Construir categorías/subcategorías, comercios, contrapartes y etiquetas: datos iniciales, edición, archivo y consolidación con referencias conservadas.
4. Implementar referencias cambiarias manuales, contexto histórico/actual y cambio de presentación base; sin inventar tasas ausentes ni cambiar la moneda de una cuenta con historia mediante una edición simple.
5. Exponer y probar mediante la API la creación, consulta, configuración, archivo y visibilidad de cuentas y catálogos.

**Cierre:** referencias iniciales no producen ingreso, desconocido no es cero y los accesos se respetan en listas/detalles. Retirar al responsable de una cuenta privada conserva e inmoviliza sin exponer ni trasladar datos. Las cuentas están listas para recibir operaciones, sin anticipar acuerdos o estados de tarjeta.

**Referencias:** producto 6.5/6.7, 8, 11, 17 y 19.2; técnico 5.2, 6, 9–10 y 13.

## Bloque 5. Operaciones y consistencia financiera

**Objetivo:** entregar el registro cotidiano sobre una única verdad financiera.

**Prerrequisitos clave:** cuentas, permisos, clasificación, referencias cambiarias y garantías transaccionales.

**Trabajo, en orden:**
1. Implementar operación/revisión, efectos de posición y componentes económicos; registro de ingresos, gastos, transferencias y comisiones, con multimoneda desde el inicio.
2. Incorporar divisiones, excepcionalidad, devoluciones, clasificación pendiente, notas, etiquetas y selecciones/sugerencias basadas en decisiones previas. Los adelantos con derecho de cobro se completan en el bloque 7.
3. Mantener saldos actuales reconstruibles e historial; implementar corrección descriptiva/financiera, anulación y reconciliación con consecuencias y revisión de antecedentes afectados.
4. Implementar búsqueda, filtros, detalle y consultas comunes de posición, ingresos/gastos y efectivo que reutilizarán presupuestos y reportes; no crear fórmulas paralelas después.
5. Exponer y probar mediante la API el registro y la corrección de operaciones, confirmaciones y recuperación de respuestas perdidas. Preservar reglas al fusionar catálogos con movimientos existentes.

**Cierre:** operaciones simples y compuestas son atómicas e idempotentes; reconstruir saldos coincide con las proyecciones. Transferir no genera ingreso/gasto, devolver reduce gasto neto y corregir historia no inventa efectivo actual. Reintentos, ediciones concurrentes y referencias restringidas están probados.

**Referencias:** producto 7–11 y 17; técnico 5–7, 9, 13 y 15.1.

## Bloque 6. Vencimientos, aplicaciones y recurrentes

**Objetivo:** construir una sola base de obligaciones y aplicaciones para recurrentes, préstamos y tarjetas.

**Prerrequisitos clave:** movimientos efectivos del bloque 5 y calendario/trabajos durables del bloque 2.

**Trabajo, en orden:**
1. Implementar vencimientos con importe y certeza, aplicaciones de movimientos, capacidad disponible, distribución entre obligaciones y recálculo por corrección/anulación.
2. Implementar series y todas sus frecuencias, versiones, vista previa, generación por horizonte, cambios, pausas, omisiones y cancelación.
3. Entregar compromisos e ingresos esperados sin cuenta con privacidad explícita, suscripciones financieras personales y equivalencias identificadas.
4. Construir agenda y recordatorios configurables, deduplicación y comprobación actual de permisos; exponer y probar mediante la API la programación, el registro/vinculación de pagos y la consulta de pendientes.

**Cierre:** varias aplicaciones no sobreutilizan un movimiento ni completan obligaciones insuficientemente cubiertas. Las reglas de meses cortos y cambios horarios pasan pruebas. Llegar a una fecha no mueve dinero. Agenda y aplicaciones están disponibles para que préstamos/tarjetas se integren sin construir otro motor.

**Referencias:** producto 15 y conceptos de 12–14; técnico 7.2–7.3, 11.1, 12.1–12.3 y 16.

## Bloque 7. Deudas, préstamos y derechos de cobro

**Objetivo:** completar los acuerdos y sus hechos financieros, distinguiendo capital, costos e incertidumbre.

**Prerrequisitos clave:** posiciones/operaciones y motor de vencimientos/aplicaciones ya cerrados.

**Trabajo, en orden:**
1. Implementar deudas simples y préstamos estructurados, condiciones conocidas, desembolsos, calendario del acuerdo e incorporación de pendientes/antecedentes.
2. Implementar pagos, desglose conocido o pendiente, anticipos de cuotas y abonos a capital; vincular a vencimientos existentes y no duplicarlos.
3. Implementar derechos por préstamos, adelantos recuperables, servicios y devoluciones pendientes; cobros, intereses y reconocimiento de servicios conforme al diseño técnico.
4. Completar condonación, pérdidas, refinanciamiento, compensación explícita, disputas y cambios de condiciones; extender correcciones, consultas y calidad del dato.
5. Exponer y probar mediante la API el detalle y los casos de uso de deuda, préstamo otorgado, adelanto y cobro, con su explicación financiera.

**Cierre:** efectivo, capital conocido, costos y cumplimiento coinciden sin doble reconocimiento. Cobrar un servicio ya reconocido no vuelve a generar ingreso. Un desglose desconocido no se convierte en capital preciso y la historia inicial no afecta dos veces las cuentas.

**Referencias:** producto 9.4, 12–13 y 19.2; técnico 5–7 y 11.1–11.2.

## Bloque 8. Tarjetas y compras en cuotas

**Objetivo:** entregar la lectura específica de tarjetas y exigibilidad financiada sobre el núcleo existente.

**Prerrequisitos clave:** operaciones, obligaciones, aplicaciones y tratamiento de costos financieros.

**Trabajo, en orden:**
1. Implementar agrupación de posiciones por moneda, límites conocidos, saldo a favor y crédito disponible.
2. Implementar ciclos/estados, cierre, vencimiento, mínimo, pagos aplicados y reconstrucciones diferenciadas de estados confirmados.
3. Implementar compras en cuotas, exigibilidad por ciclo, devoluciones, cargos y pagos parciales/completos; usar el motor de vencimientos sin crear recurrentes duplicados.
4. Exponer y probar mediante la API consultas, correcciones y operaciones de tarjeta/pago, verificando el efecto de un cargo recurrente y su pago posterior.

**Cierre:** compra más pago reconoce gasto una sola vez; deuda actual, pendiente del estado, mínimo y cuotas posteriores son distinguibles. Multimoneda y sobrepagos no incrementan artificialmente el efectivo.

**Referencias:** producto 14 y 15.6; técnico 5–7, 11.3 y 12.2.

## Bloque 9. Documentos e incorporación de información

**Objetivo:** completar las formas de iniciar, respaldar e incorporar información cuando sus destinos financieros ya existen.

**Prerrequisitos clave:** cuentas, movimientos, acuerdos, tarjetas, vencimientos y privacidad implementados.

**Trabajo, en orden:**
1. Implementar archivos privados, metadatos, búsqueda, cuarentena/validación, descarga autorizada y documentos independientes o relacionados con recursos existentes.
2. Completar incorporación guiada con referencias iniciales, antecedentes, acuerdos, estados y compromisos; reutilizar las capacidades construidas sin volver a aplicar hechos históricos.
3. Implementar importación tabular: mapeo, staging, vista previa, errores/duplicados, confirmación, lotes atómicos, recuperación e incorporación mediante los mismos casos de uso.
4. Implementar reversión controlada y exportación de registros con snapshot, alcance, protección CSV, estado consultable, expiración y limpieza. La exportación de resultados de reportes se integra en el bloque 11 usando este mecanismo.
5. Verificar estos recorridos mediante la API y aplicar desde este bloque las reglas de retención y revocación de archivos/derivados.

**Cierre:** subir un documento no cambia saldos ni concede permiso al asistente; documentos sin cuenta son privados por defecto. Importar/reintentar no duplica ni omite silenciosamente y revertir respeta relaciones posteriores. La exportación real se descarga con acceso vigente y caduca.

**Referencias:** producto 19 y 22.1–22.3; técnico 9.3, 12.1, 14 y 17.2.

## Bloque 10. Presupuestos, reservas y proyecciones

**Objetivo:** calcular planificación sobre todas las fuentes financieras y de compromisos ya disponibles.

**Prerrequisitos clave:** consultas económicas del bloque 5, agenda, acuerdos, tarjetas e incorporación de datos.

**Trabajo, en orden:**
1. Implementar presupuestos por categoría/período, permisos y consumo neto con devolución, excepcionalidad e historial vigente.
2. Implementar reservas, fondos asignados y cobertura de compromisos; distinguir intención no asignada de disponibilidad concreta.
3. Implementar proyección por cuenta/moneda y horizontes definidos, clasificación de certeza, escenarios conservadores, datos faltantes y explicación de insuficiencias.
4. Exponer y probar consultas y acciones mediante la API, reutilizando consultas/fechas/permisos y comprobando que reservas, cargos y pagos no descuentan dos veces el mismo efectivo.

**Cierre:** presupuesto no se confunde con saldo ni reserva con transferencia. Proyecciones son reproducibles, no crean hechos y expresan límites de información; lectores y participantes sin acceso no obtienen totales privados.

**Referencias:** producto 16–17; técnico 6, 12 y 13.

## Bloque 11. Reportes, calidad de datos e inicio

**Objetivo:** completar las lecturas del producto con evidencia común y el inicio financiero integrado.

**Prerrequisitos clave:** todas las fuentes financieras, planificación y mecanismo de exportación de registros.

**Trabajo, en orden:**
1. Completar todos los reportes del apartado 18 del producto, comparaciones, tendencias, originales/consolidados y detalle navegable con consultas semánticas compartidas.
2. Unificar la calidad de datos y acciones de revisión provenientes de operaciones, acuerdos, conversiones, reconciliaciones y vencimientos.
3. Incorporar exportación de resultados reutilizando snapshot, contexto, permisos y expiración del bloque 9.
4. Implementar las consultas del inicio con liquidez/actualización, situación del período, próximos/vencidos y tareas de revisión; exponer datos y referencias de detalle para que los clientes construyan reportes, gráficos y navegación sin duplicar cálculos financieros.

**Cierre:** cada cifra llega a sus movimientos o supuestos; filtros de resumen, detalle y exportación coinciden. Comparaciones y conversiones incompletas son explícitas. Las consultas están listas para el asistente sin reconstruir cálculos ni incluir cuentas privadas.

**Referencias:** producto 18 y 21; técnico 5–6, 9.3, 12.4, 13–14 y 19.1.

## Bloque 12. Asistente de inteligencia artificial

**Objetivo:** añadir interpretación y propuestas sin convertir al modelo en autoridad financiera o de permisos.

**Prerrequisitos clave:** casos de uso financieros completos y consultas verificables del bloque 11.

**Trabajo, en orden:**
1. Implementar consentimiento, conversaciones privadas por usuario/espacio, límites y minimización de datos, con revocación sobre historia y resúmenes derivados.
2. Exponer herramientas tipadas de consulta sobre los mismos servicios y resultados que los reportes; respuestas con evidencia y advertencias.
3. Implementar propuestas persistidas, datos faltantes, revisión de efectos, confirmación efectiva del cliente, versiones/caducidad e idempotencia de ejecución.
4. Verificar mediante la API los recorridos del asistente, errores, contenido malicioso, cambios de permisos/datos, eliminación de conversaciones y continuidad sin IA.

**Cierre:** cifras con los mismos filtros coinciden con reportes; no existe escritura sin confirmación y revalidación. Reintentos no repiten operaciones y un fallo nunca se presenta como éxito. No se usa SQL del modelo ni se incluye lectura documental fuera de alcance.

**Referencias:** producto 20–21; técnico 9.3, 15.2, 17.2 y 18.

## Bloque 13. Administración y ciclo de vida completo

**Objetivo:** cerrar gestión del servicio y privacidad cuando todas las clases de datos y procesos ya existen.

**Prerrequisitos clave:** identidad interna y capacidades del bloque 3; recursos, derivados y trabajos de todos los bloques funcionales.

**Trabajo, en orden:**
1. Completar contratación por espacio y sus confirmaciones verificadas, cambios e historial, consumo y responsabilidad de facturación, integrando el control ya usado por invitaciones, archivos e IA. Mantener separación de finanzas personales y entre espacios.
2. Completar las capacidades administrativas mediante la API: consulta de estados, gestión autorizada de usuarios/espacios, sesiones, capacidades, incidencias y trabajos fallidos, sin facultades financieras implícitas.
3. Implementar casos/concesiones de soporte, alcance temporal, revocación, auditoría y dobles aprobaciones sensibles; sin suplantación ni edición financiera ordinaria por soporte.
4. Completar exportación de datos personales, solicitudes de eliminación de usuario/espacio, resolución de responsabilidades, seudonimización, purga y excepciones justificadas. Exponer y probar la creación y el seguimiento de solicitudes mediante la API.
5. Verificar retención de cada recurso, registro de eliminaciones y restauración coordinada con archivos, derivados y trabajos; completar procedimientos e información de privacidad y soporte exigidos por el producto.

**Cierre:** el personal trabaja mediante facultades restringidas y accesos auditados que vencen; eliminar no borra hechos compartidos ni expone cuentas privadas. Restaurar reaplica eliminaciones antes de habilitar accesos/trabajos. La contratación y sus consumos funcionan con confirmaciones reales, no solo fixtures o respuestas simuladas.

**Referencias:** producto 6.1/6.7/6.9 y 22.3–22.4; técnico 8–10 y 16–18.

## Bloque 14. Verificación integral y cierre

**Objetivo:** demostrar que la suma de capacidades satisface el alcance del servidor de ambos diseños sin contradicciones ni huecos.

**Prerrequisito clave:** todos los bloques anteriores cerrados; ninguna funcionalidad prevista se inicia por primera vez aquí.

**Trabajo, en orden:**
1. Ejecutar los casos del apartado 24 del producto aplicables al servidor y las precisiones del diseño técnico, enlazando cada requisito con evidencia de prueba y recorrido real mediante la API.
2. Ejecutar recorridos completos mediante las API de clientes y administración: alta, colaboración, registro de operaciones, pagos/cobros, reconciliación, incorporación, planificación, reportes, asistente y solicitudes de datos. No requieren desarrollar ni disponer de una aplicación cliente.
3. Verificar permisos y revocación entre todos los recursos, acceso interno, ediciones simultáneas, sobreaplicación, reintentos e interrupciones antes/después de confirmar.
4. Medir carga, consultas y memoria con datos sintéticos representativos; realizar análisis de vulnerabilidades, fuzzing y restauración ensayada. Contrastar resultados con los objetivos y límites documentados.
5. Resolver incidencias, repetir la evidencia afectada y entregar contratos, documentación operativa y matriz final de cobertura actualizados.

**Cierre:** cobertura completa del alcance del servidor, validaciones reproducibles y sin defectos que impidan resultados financieros correctos, privacidad, recuperación o casos de uso previstos. Las decisiones e integraciones del backend necesarias están resueltas; funcionalidades simuladas o meramente listadas no cuentan como terminadas. Los contratos y la documentación permiten al otro equipo integrar el cliente sin trasladarle reglas financieras. El cierre no incluye desarrollo de clientes ni ampliaciones no comprometidas, ni convierte el diseño en inmutable.

**Referencias:** producto 1–24, especialmente 21–24; técnico 1–19.

## 4. Comprobación de dependencias y cobertura

### Dependencias que determinan el orden

| Capacidad necesaria | Debe estar disponible antes de |
| --- | --- |
| Preparación y validación técnica (1–2) | Cualquier funcionalidad de negocio. |
| Correo, trabajos y auditoría (2) | Verificación, recuperación e invitaciones (3); recordatorios (6). |
| Identidad, membresías y capacidades (3) | Crear/operar recursos financieros (4 en adelante). |
| Dinero/calendario base (2), cuentas y clasificación (4) | Registro de hechos y referencias monetarias (5). |
| Operaciones y consultas económicas (5) | Aplicar pagos (6), acuerdos (7), tarjetas (8), presupuestos (10). |
| Vencimientos y aplicaciones (6) | Calendarios/pagos de préstamos (7) y ciclos/cuotas de tarjetas (8). |
| Todos los destinos financieros y agenda (4–8) | Incorporación guiada e importación completas (9). |
| Todas las fuentes de hechos y expectativas (4–9) | Planificación consolidada (10) y reportes completos (11). |
| Consultas verificables y evidencia (11) | Asistente (12). |
| Todos los recursos y derivados (4–12) | Eliminación, soporte y restauración integrales (13). |
| Todos los bloques de construcción (1–13) | Aceptación integral del backend (14). |

### Asignación del alcance

| Fuente | Bloques responsables |
| --- | --- |
| Producto 1–5: visión, principios, plataformas y conceptos | Guían todo el plan; preparación en 1–2 y verificación en 14. |
| Producto 6: identidad, espacios, colaboración y contratación | 3; privacidad de cuentas y salidas en 4; integración de recursos en cada bloque; cierre integral en 13. |
| Producto 7–10: lecturas financieras, cuentas, operaciones y reconciliación | 4–5; casos especializados en 7–8. |
| Producto 11: clasificación y contrapartes | 4–5. |
| Producto 12–14: deudas, cobros y tarjetas | Base de aplicaciones en 6; entrega específica en 7–8. |
| Producto 15: recurrentes, suscripciones y agenda | 6, con integración de orígenes en 7–8. |
| Producto 16: presupuesto, reservas y proyección | 10. |
| Producto 17: multimoneda | Fundamentos en 2, cuentas/referencias en 4 y operaciones en 5; aplicada en todos los consumidores posteriores. |
| Producto 18: reportes y calidad de datos | Consultas base en 5, avisos desde cada origen y entrega unificada en 11. |
| Producto 19: documentos e incorporación | Referencias/antecedentes desde 4–8; casos de uso completos del servidor en 9. |
| Producto 20: asistente | 12. |
| Producto 21: experiencia y flujos del producto | Contratos y casos de uso del servidor que los soportan en cada bloque; consultas de inicio/reportes en 11 y aceptación del backend en 14. La interfaz cliente queda fuera de este plan. |
| Producto 22: protección, confiabilidad, datos y administración | Garantías desde 2–3 y cada recurso; cierre de administración/privacidad en 13 y verificación en 14. |
| Producto 23–24: límites y aceptación | Exclusiones respetadas en todo el plan; pruebas progresivas y aceptación del alcance del servidor en 14. |
| Técnico 1–4: evolución, principios, stack y arquitectura | Reglas del plan y bloques 1–2; aplicables durante todo el desarrollo. |
| Técnico 5–7: núcleo, dinero y consistencia | Fundamentos en 2, implementación financiera en 4–5 y extensiones en sus bloques. |
| Técnico 8–10: identidad, permisos y conservación restringida | 3–4, integración obligatoria en cada recurso y cierre de ciclo de vida en 13. |
| Técnico 11–13: acuerdos, planificación y consultas | 6–8 y 10–11. |
| Técnico 14–16: incorporación, API, IA y trabajos | Base HTTP/trabajos en 2, contratos en cada bloque, incorporación en 9 y asistente en 12. |
| Técnico 17–19: administración, retención, seguridad y calidad | Implementación progresiva; cierre de ciclo de vida en 13 y evidencia integral en 14. |

Al comenzar un bloque se vinculan sus requisitos concretos a tareas y pruebas; al cerrarlo se registra la evidencia y se marca su estado en la secuencia. Esta trazabilidad se mantiene en las herramientas de trabajo del equipo, sin convertir este documento en otra especificación funcional.
