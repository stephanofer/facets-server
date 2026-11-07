# Diseño de producto — Gestión financiera personal y compartida

## 1. Propósito y carácter del documento

Este documento define qué producto se quiere construir, qué necesidades resuelve, qué funcionalidades incluye y qué reglas deben respetar sus resultados. Está dirigido al equipo de producto y servirá como referencia para elaborar una guía de desarrollo independiente.

**Es un diseño evolutivo, no un contrato inmutable.** Se puede corregir, ampliar, simplificar, reorganizar o reemplazar cualquier parte cuando exista una razón que beneficie al producto. Durante el diseño detallado, el desarrollo y el uso real pueden aparecer necesidades, errores o casos no considerados. El equipo podrá acordar los cambios y actualizar este documento sin quedar atado a decisiones anteriores.

Los cambios deben explicitar qué comportamiento reemplazan y sus consecuencias sobre datos, privacidad, experiencia y resultados financieros. La flexibilidad no significa mantener reglas contradictorias ni cambiar el significado de los números sin explicarlo.

Este documento no establece fases, fechas, prioridades de construcción, estructura de código ni instrucciones de implementación. Las posibilidades de ampliación se identifican como tales: no son funcionalidades comprometidas por el alcance definido.

## 2. Visión y necesidades que resuelve

El producto es un sistema de control financiero personal y compartido. Integra registro de movimientos, cuentas, deudas, dinero por cobrar, compromisos, presupuestos, reportes y un asistente de inteligencia artificial con acceso autorizado a la información vigente.

Debe permitir responder:

- Cuánto dinero hay, dónde está y en qué monedas.
- Cuánto se gana y se gasta, y en qué se utiliza el dinero.
- Cuánto se debe, a quién, qué se ha pagado y qué queda pendiente.
- Cuánto deben otras personas o entidades y qué se ha recuperado.
- Qué pagos se aproximan o están atrasados.
- Si el dinero disponible alcanza para los compromisos previstos.
- Qué decisiones o hábitos conviene revisar y con qué evidencia.

Está orientado a personas, parejas, familias y grupos con varias cuentas, tarjetas, obligaciones o monedas, sin pretender ser un sistema de contabilidad empresarial. El uso personal cotidiano es el punto de validación; el producto debe admitir otros usuarios con información independiente y una operación pública de calidad.

Perú es la referencia de mercado del documento original, pero no se ha reconfirmado como decisión comercial definitiva. Países, precios y condiciones de lanzamiento requieren definición específica. El comportamiento financiero no debe quedar limitado innecesariamente a una moneda o país.

## 3. Principios del producto

- **Claridad financiera:** no todo ingreso de dinero es un ingreso ganado ni toda salida es un gasto.
- **Registro bajo control humano:** crear un compromiso o alcanzar una fecha nunca autoriza un movimiento financiero.
- **Una sola verdad financiera:** cuentas, reportes, presupuestos, proyecciones y asistente utilizan las mismas reglas y operaciones.
- **Simplicidad de uso:** mostrar primero lo necesario y permitir profundizar sin exigir conocimientos contables.
- **Trazabilidad:** cada cifra debe poder explicarse mediante sus movimientos, ajustes y supuestos.
- **Incertidumbre explícita:** un dato desconocido no se sustituye por cero ni una estimación se presenta como confirmación.
- **Privacidad por contexto:** pertenecer a un espacio no concede acceso irrestricto a sus cuentas.
- **Captura móvil práctica:** registrar y corregir información desde el teléfono debe ser rápido y comprensible.
- **Historia conservada:** cerrar cuentas, cancelar compromisos o retirar miembros no borra hechos financieros.
- **IA subordinada al producto:** interpreta, consulta y prepara acciones; no decide permisos ni inventa cálculos.

## 4. Plataformas y sistema central

Las decisiones tecnológicas expresamente establecidas son:

- Backend central desarrollado en Go.
- Primer cliente: aplicación Android nativa con Kotlin y Jetpack Compose.

El backend concentra identidad, membresías, permisos, contratación por espacio, operaciones, saldos, deudas, conversiones, presupuestos, reportes, proyecciones y capacidades financieras del asistente. La aplicación permite acceder a esas funciones y adapta su presentación al teléfono; no mantiene definiciones financieras independientes.

El producto podrá tener otros clientes que utilicen el mismo sistema central. Sus interfaces no necesitan ser idénticas, pero una misma operación debe tener el mismo significado y resultado desde cualquier cliente.

La administración interna dispone de un ámbito de acceso separado de la experiencia financiera de clientes. Esta separación no obliga a utilizar otro backend o base de datos; el canal y las tecnologías de su interfaz no se definen en este documento.

No se fijan aquí otras herramientas, infraestructura o tecnologías.

## 5. Conceptos principales

| Concepto | Significado |
| --- | --- |
| Cuenta de usuario | Identidad de una persona para utilizar los espacios financieros del producto. |
| Identidad interna | Acceso individual del personal que administra el servicio, separado del acceso como cliente. |
| Espacio financiero | Contexto independiente que organiza información personal, familiar o de un grupo. |
| Membresía | Pertenencia de un usuario a un espacio, con un rol propio de ese contexto. |
| Plan del espacio | Capacidades y límites comerciales disponibles para ese espacio. |
| Suscripción comercial | Contratación del servicio para un espacio, con su estado, vigencia e historial propios. |
| Responsable de facturación | Persona autorizada para administrar la contratación y sus comunicaciones; no equivale necesariamente al propietario. |
| Cuenta financiera | Posición de dinero, deuda o derecho de cobro en una moneda. |
| Operación financiera | Un hecho registrado y sus efectos relacionados sobre cuentas y obligaciones. |
| Contraparte | Persona, comercio, banco, empresa u otra entidad involucrada. |
| Acuerdo financiero | Condiciones y antecedentes de una deuda o derecho de cobro. |
| Serie recurrente | Configuración manual de un compromiso o ingreso que se repite. |
| Vencimiento | Instancia concreta de un pago o cobro esperado. |
| Aplicación de pago | Relación entre un movimiento real y la obligación que satisface, por un importe determinado. |
| Presupuesto | Plan de gasto para un período y sus categorías. |
| Reserva | Asignación conceptual de dinero para una finalidad, sin crear una transferencia. |
| Reconciliación | Comparación del saldo calculado con un saldo observado en una fecha. |

Una deuda puede aparecer en cuentas, agenda, reportes y su detalle sin tener saldos independientes. Del mismo modo, un pago puede actualizar una cuenta, una deuda y un vencimiento sin que el usuario registre varias veces el mismo hecho.

## 6. Usuarios, espacios y colaboración

### 6.1. Identidad y acceso

Cada persona tiene una sola cuenta de usuario del producto y sus propias credenciales. Si también forma parte del personal interno, utiliza una identidad y sesión administrativas separadas, según el apartado 22.4. Se incluye:

- Registro y verificación de correo.
- Inicio y cierre de sesión.
- Recuperación de acceso y cambios de credenciales.
- Segundo factor de autenticación.
- Consulta y revocación de sesiones de otros dispositivos.
- Preferencias personales de presentación y notificaciones.
- Gestión de exportación y solicitudes de eliminación de datos.

Los integrantes de una familia o grupo no comparten contraseñas. Registrar una contraparte o una persona que debe dinero no crea un usuario ni le concede acceso.

### 6.2. Múltiples espacios desde el nacimiento del producto

Una cuenta de usuario puede crear o integrar varios espacios simultáneamente. Por ejemplo: un espacio personal, uno familiar y uno de viaje con amigos.

- Al registrarse, la persona dispone de un espacio personal privado.
- Puede crear otros espacios, invitar personas a cualquiera de ellos según sus facultades y límites vigentes, y aceptar invitaciones a otros.
- Aceptar una invitación agrega una membresía; no reemplaza las anteriores ni traslada datos.
- Un espacio se utiliza individualmente cuando tiene un solo integrante y pasa a compartirse mediante invitaciones aceptadas. No se crea otro espacio ni se migran datos para hacerlo compartido.
- Personal, familia y grupo describen su finalidad, no estructuras diferentes ni restricciones de colaboración. Familia puede representar también una pareja.
- Invitar o cambiar la finalidad conserva el mismo espacio, sus cuentas, movimientos, documentos, historial, configuración y contratación. No cambia por sí solo el plan, el responsable de facturación ni los permisos existentes sobre sus cuentas.

Cada espacio mantiene sus cuentas, movimientos, categorías, comercios, contrapartes, documentos, deudas, compromisos, presupuestos y reportes. Una cuenta financiera pertenece a un único espacio: no se comparte su saldo entre espacios ni se copia automáticamente al aceptar una invitación.

La identidad pertenece a la persona; la información financiera y la contratación del servicio se organizan por espacio. Un usuario puede ser propietario en uno, administrador en otro y miembro en un tercero, cada uno con un plan distinto.

La pertenencia a un grupo no convierte al usuario en propietario de todo su dinero ni en deudor de todos sus gastos. No existe reparto o compensación automática entre integrantes.

### 6.3. Configuración del espacio

- Nombre y finalidad.
- Moneda base de presentación.
- Zona horaria y preferencias regionales.
- Inicio del período operativo.
- Preferencias de colaboración y visibilidad de cuentas nuevas.
- Administración de categorías y otros datos compartidos.

Los períodos pueden ser calendario u operativos. Por ejemplo, un período puede comenzar el día 26 para alinearse con el cobro del sueldo. Su inicio y fin deben estar visibles; los días inexistentes en ciertos meses necesitan una regla de calendario explícita, no una fecha inválida ni períodos superpuestos.

Cambiar una preferencia no altera silenciosamente la historia. Si se recalculan agrupaciones o presentaciones, se explica su efecto.

### 6.4. Roles por espacio

| Rol | Facultades |
| --- | --- |
| Propietario | Responsable del espacio. Puede transferir su propiedad y autorizar su eliminación. |
| Administrador | Gestiona configuración, integrantes e invitaciones dentro de sus facultades. |
| Miembro | Colabora sobre la información financiera para la que tiene permisos. |
| Lector | Consulta la información autorizada sin modificarla. |

El rol pertenece a la membresía: una persona puede administrar un espacio y ser lectora en otro. Gestionar integrantes no concede por sí solo acceso a cuentas privadas. Un lector no puede recibir capacidad operativa mediante una excepción de cuenta mientras conserve ese rol.

Los roles de administración de la plataforma están separados de los roles financieros de los espacios. Ninguna membresía, invitación, transferencia de propiedad o contratación concede facultades internas. Administrar la facturación de un espacio requiere autorización específica y no habilita acceso a cuentas privadas.

### 6.5. Privacidad y permisos de cuentas financieras

Una cuenta puede ser:

- Privada.
- Compartida con todo el espacio.
- Compartida con integrantes específicos del mismo espacio.

Para los integrantes autorizados se distinguen dos permisos:

- **Consultar:** ver saldos, movimientos y detalles permitidos.
- **Operar:** registrar y corregir operaciones permitidas de esa cuenta.

Cambiar accesos, transferir responsabilidad o eliminar una cuenta son acciones de administración, no consecuencias de poder registrar gastos. El responsable registrado de una cuenta no equivale necesariamente al titular legal de la cuenta bancaria.

El responsable administra configuración, accesos, archivo y transferencia de responsabilidad. Compartir concede consulta u operación, no administración delegada. Los roles del espacio no sustituyen esa responsabilidad.

Cuando un espacio tiene varios integrantes, las cuentas nuevas se proponen como compartidas, mostrando esa elección antes de confirmar y respetando la preferencia configurada. Puede restringirse su visibilidad. Cambiar esa preferencia o incorporar al primer invitado no convierte cuentas privadas existentes en compartidas.

Una cuenta compartida con todo el espacio será accesible a quienes se incorporen, con el permiso que corresponda. La invitación debe explicar este efecto; las cuentas privadas no se comparten por la incorporación de un miembro.

Los permisos alcanzan listas, totales, búsquedas, documentos, reportes, exportaciones, notificaciones, proyecciones y asistente. No debe filtrarse una cuenta privada mediante una cifra agregada, una referencia de otra operación o una conversación compartida.

### 6.6. Invitaciones

El flujo consiste en invitar por correo, elegir rol, mostrar el espacio de destino y solicitar aceptación explícita al destinatario autenticado con ese correo verificado.

Se incluye:

- Invitaciones pendientes, aceptadas, rechazadas, canceladas y vencidas.
- Reenvío controlado e invalidación del enlace reemplazado.
- Invitaciones simultáneas a espacios diferentes.
- Prevención de membresías duplicadas y de aceptaciones repetidas.
- Verificación de que el remitente siga autorizado al aceptar.
- Prohibición de conceder roles superiores a las facultades de quien invita.
- Comprobación de que el plan, el estado comercial y los límites vigentes permitan incorporar al destinatario. La aceptación vuelve a validar estas condiciones aunque la invitación se haya enviado antes.

Si no se puede completar la incorporación, se explica el motivo y cómo resolverlo, sin crear una membresía parcial ni modificar las otras pertenencias del destinatario. Recibir una invitación no obliga al invitado a contratar una suscripción individual adicional.

### 6.7. Retiro, salida y propiedad

- Retirar a un integrante revoca su membresía y accesos a ese espacio, no elimina su usuario ni otras pertenencias.
- Las operaciones previas permanecen con su autoría histórica.
- El creador de un registro no puede llevárselo ni borrarlo por abandonar el espacio.
- El propietario debe transferir la propiedad antes de salir de un espacio activo.
- Debe mantenerse una responsabilidad administrativa vigente; no se permite dejar el espacio sin una persona autorizada para gestionarlo.
- Las cuentas compartidas deben conservar un responsable vigente.
- Las cuentas privadas no se hacen públicas ni se trasladan automáticamente por una salida.
- Retirar o incorporar integrantes no cancela, transfiere ni reemplaza por sí solo la contratación del espacio. Si sale su responsable de facturación, debe resolverse explícitamente esa responsabilidad y la continuidad o cancelación del servicio, sin asumir autorización para utilizar medios de pago ajenos.
- Eliminar un espacio es una acción distinta, sensible y confirmada, con explicación de sus consecuencias.

Eliminar un espacio requiere resolver también su contratación y los compromisos comerciales pendientes, sin dejar cobros activos sin referencia ni borrar registros que deban conservarse conforme a las condiciones informadas.

La salida con cuentas privadas bajo responsabilidad del integrante conserva e inmoviliza esas cuentas sin exponer datos, borrar historia ni trasladar posiciones. Las precisiones de conservación y regularización se definen en el apartado 10 del diseño técnico. Reincorporarse no restaura accesos: el antiguo responsable debe confirmar explícitamente la recuperación de su propia responsabilidad, con identidad y autenticación vigentes; no recupera otras concesiones terminadas.

### 6.8. Selector y contexto activo

El usuario cambia de espacio mediante un selector visible. Cuentas, formularios, reportes y conversaciones muestran el contexto correspondiente. Una operación iniciada en un espacio no cambia de destino por navegar a otro.

Las notificaciones abren el espacio correcto después de comprobar el acceso. Cada cliente conserva su selección localmente y cada dispositivo puede tener un espacio activo diferente. El servidor comprueba el acceso al espacio indicado en cada solicitud, sin almacenar la selección. No se incluye un panel consolidado entre todos los espacios.

### 6.9. Planes y suscripciones comerciales por espacio

#### 6.9.1. Unidad de contratación

**Cada espacio tiene su propio plan y estado comercial. La contratación no pertenece globalmente al usuario ni se extiende a todos sus espacios.** Esta capacidad forma parte del diseño del producto, no es una ampliación hipotética.

- Un espacio individual y uno familiar pueden contratar planes diferentes.
- Los integrantes utilizan las capacidades del espacio según sus permisos, sin contratar cada uno una suscripción individual adicional para participar.
- Si el precio depende de integrantes u otra medida, el cargo corresponde al espacio; la fórmula y los importes siguen pendientes de definición.
- El beneficio de un plan se utiliza en el espacio que lo tiene, no en otros espacios de sus miembros.
- Abandonar un espacio no lleva su plan a otro ni cancela las contrataciones de terceros.
- Un problema de pago de un espacio no bloquea por ese motivo los demás espacios de un usuario.

#### 6.9.2. Información y gestión comercial

Cada espacio mantiene:

- Plan vigente, capacidades y límites aplicables.
- Estado comercial, diferenciando contratación vigente, prueba si existe, pago pendiente, cancelación y otras situaciones que se definan.
- Período de vigencia y condiciones de renovación o terminación.
- Responsable de facturación y comunicaciones comerciales.
- Historial de contratación, cambios de plan, pagos y comprobantes correspondientes.
- Consumo de capacidades limitadas, por ejemplo integrantes, almacenamiento o uso de IA, cuando el plan establezca esas restricciones.

El propietario puede ser responsable de facturación, pero ambas responsabilidades son diferentes. Transferir la propiedad no cambia silenciosamente quién paga ni autoriza utilizar el medio de pago de otra persona. La gestión comercial se limita a personas expresamente autorizadas y no expone credenciales ni datos completos del medio de pago.

Los cambios y renovaciones deben dejar un único plan efectivo identificable para el período correspondiente. Un reintento de contratación o una confirmación repetida no crea otra contratación ni aplica dos veces el mismo cambio. Los derechos de uso dependen del estado comercial verificado, no solo de un mensaje de éxito mostrado en el cliente.

#### 6.9.3. Capacidades y permisos

Para realizar una acción deben cumplirse, por separado:

1. Identidad y membresía vigentes en el espacio de destino.
2. Permiso suficiente para la acción y los datos involucrados.
3. Capacidad disponible conforme al plan, estado comercial y límite aplicable.

Pagar un plan no concede acceso a cuentas privadas. Ser administrador no elimina los límites comerciales. El asistente, las importaciones y los demás clientes respetan los mismos controles; una acción no evita un límite por realizarse desde otra pantalla.

Los consumos se atribuyen al espacio correspondiente. Un mismo usuario no mezcla cuotas o beneficios entre espacios y una acción repetida no consume dos veces la misma asignación confirmada. Las reglas exactas de medición y reposición deben definirse junto con cada límite comercial.

#### 6.9.4. Cambios, cancelación y conservación

- Cambiar de plan no traslada datos ni modifica membresías o permisos financieros.
- Una reducción de límites no borra automáticamente información excedente ni elimina integrantes sin un procedimiento explícito.
- Cancelar una suscripción o registrar falta de pago no equivale a eliminar el espacio o su historia.
- Debe explicarse qué capacidades se mantienen o restringen, y bajo qué condiciones pueden consultarse o exportarse datos.
- Se conserva el historial comercial necesario y se aplican las políticas informadas de retención y privacidad.

Precios, nombres de planes, límites, pruebas, períodos de gracia, fechas de aplicación de cambios y condiciones de consulta, exportación y conservación tras finalizar el servicio siguen pendientes. No se presupone acceso indefinido ni eliminación inmediata.

#### 6.9.5. Separación de las finanzas registradas

La suscripción comercial del espacio es distinta de las suscripciones que sus integrantes registran para controlar gastos. Nuestra contratación puede tener renovaciones y confirmaciones automáticas según lo contratado, sin crear movimientos dentro de las cuentas financieras del usuario.

Registrar ese gasto en el gestor sigue siendo una acción humana confirmada. Ningún cambio comercial descuenta saldos financieros, marca compromisos personales como pagados ni autoriza transferencias bancarias.

El proveedor y canal de cobro no están elegidos. La contratación desde Android debe evaluarse según la distribución y las políticas aplicables

## 7. Las tres lecturas financieras

El producto distingue estas lecturas en nombres, cálculos y explicaciones:

| Lectura | Pregunta | Ejemplos |
| --- | --- | --- |
| Ingresos y gastos | ¿Qué ingresos se registraron y qué se consumió en el período? | Sueldo, compras, intereses y comisiones. |
| Entradas y salidas de efectivo | ¿Qué dinero entró o salió de las cuentas líquidas? | Cobros, pagos de tarjetas, préstamos recibidos y devolución de capital. |
| Compromisos y disponibilidad futura | ¿Qué se espera pagar o cobrar y cómo quedaría el dinero? | Vencimientos, ingresos esperados, reservas y proyecciones. |

Ejemplo: comprar alimentos por S/ 120 con tarjeta genera un gasto y aumenta la deuda. Pagar esos S/ 120 después reduce efectivo y deuda; no genera otro gasto por la compra.

Una proyección no modifica saldos. El crédito disponible y el dinero que otros deben no se suman al dinero líquido.

## 8. Cuentas financieras y saldos

### 8.1. Tipos de posiciones

- Dinero líquido: efectivo, cuentas bancarias, ahorros y billeteras con saldo independiente.
- Obligaciones: tarjetas, deudas simples, préstamos y compras financiadas fuera de tarjeta.
- Derechos de cobro: préstamos otorgados, adelantos recuperables y otros cobros identificados por su origen.

Las deudas y cobros tienen experiencias específicas, pero su posición no se duplica por aparecer en el resumen de cuentas.

### 8.2. Información y acciones

Cada cuenta incluye nombre, tipo, moneda, institución o contraparte cuando corresponda, responsable, permisos, fecha de seguimiento, saldo inicial, saldo actual, historial y estado.

El usuario puede crearla, consultar su evolución, corregir información, reconciliar saldos y cerrarla o archivarla. El cierre o archivo impide su uso cotidiano, pero conserva su participación histórica en reportes.

Una billetera que solo sirve como medio de pago sobre una cuenta bancaria no duplica ese dinero. Si mantiene saldo independiente, corresponde una cuenta propia.

### 8.3. Reglas de saldo

- Cada posición tiene una moneda definida.
- El saldo inicial tiene fecha y representa una referencia, no un ingreso o gasto del período.
- El saldo actual se explica mediante esa referencia y los hechos posteriores.
- Un saldo negativo no se convierte artificialmente en cero.
- Un saldo desconocido no se presenta como cero confirmado.
- La última reconciliación y las diferencias pendientes deben ser consultables.
- Cambiar la moneda de una cuenta con historial requiere un proceso controlado; no es una edición de etiqueta.
- No se necesitan números completos de cuentas o tarjetas para llevar un registro manual; alias y últimos dígitos opcionales aportan identificación suficiente.

## 9. Registro y tratamiento de operaciones

### 9.1. Captura manual

Para un gasto sencillo se solicitan importe, cuenta, fecha y categoría, con posibilidad de dejar la clasificación pendiente. Comercio, contraparte, etiquetas, nota, marca excepcional y comprobante son opcionales según el caso.

Las acciones específicas permiten registrar ingresos, transferencias, pagos de tarjetas, préstamos recibidos, pagos de deudas, préstamos otorgados, cobros, devoluciones y ajustes. Cada formulario muestra únicamente los datos pertinentes.

Se pueden recordar selecciones recientes o sugerir una clasificación basada en decisiones anteriores. Las sugerencias son visibles y corregibles: no reescriben movimientos ni representan detección de recurrentes.

### 9.2. Significado financiero

| Operación | Ingresos y gastos | Efectivo o posición |
| --- | --- | --- |
| Cobrar sueldo | Ingreso. | Aumenta dinero líquido. |
| Comprar con débito o efectivo | Gasto. | Disminuye dinero líquido. |
| Comprar con tarjeta o financiación | Gasto por la compra. | Aumenta la obligación. |
| Transferir entre cuentas propias del espacio | No genera ingreso o gasto. | Redistribuye dinero. |
| Pagar tarjeta | No repite el gasto de las compras. | Reduce efectivo y obligación por el importe aplicado. |
| Recibir un préstamo | No es ingreso ganado. | Aumenta efectivo y deuda. |
| Devolver capital | No es gasto de consumo. | Reduce efectivo y capital pendiente. |
| Registrar intereses, seguros o cargos | Costo financiero, reconocido una sola vez. | Aumenta obligación o reduce efectivo, según el hecho. |
| Prestar dinero | No es gasto de consumo. | Reduce efectivo y aumenta derecho de cobro. |
| Recuperar capital prestado | No es ingreso ganado. | Aumenta efectivo y reduce derecho de cobro. |
| Cobrar intereses | Ingreso financiero, reconocido una sola vez. | Aumenta efectivo o cancela un derecho ya registrado. |
| Reconciliar mediante ajuste | No es ingreso o gasto ordinario. | Corrige la posición y conserva la diferencia identificada. |

Una compra financiada genera gasto cuando se realiza; recibir o devolver el financiamiento no repite ese gasto. Si un costo o ingreso ya se registró antes de su pago o cobro, la cancelación posterior no vuelve a reconocerlo.

### 9.3. Gastos excepcionales

La excepcionalidad es una marca, no un tipo de operación excluyente. Una compra puede ser gasto de vivienda, pagado con tarjeta y excepcional al mismo tiempo.

- Permanece en el gasto total real y, por defecto, en su presupuesto.
- Puede excluirse de lecturas específicas de hábitos o promedios.
- Las vistas indican si incluyen o excluyen excepcionales.
- No desaparece del historial ni altera su significado por cambiar de vista.

### 9.4. División, devoluciones y reembolsos

- Una compra puede dividirse entre categorías; sus partes deben sumar el total y el saldo cambia una sola vez.
- Las devoluciones se registran con su fecha e importe reales y se vinculan con la compra cuando sea posible.
- Una devolución reduce el gasto neto correspondiente, no se trata como sueldo ni reescribe silenciosamente un período anterior.
- En un adelanto recuperable se separa el consumo propio del derecho de cobro generado.
- Cobrar un adelanto no genera ingreso ordinario por recuperar dinero propio.

### 9.5. Búsqueda, corrección y anulación

Se permite buscar y filtrar por período, cuenta, importe, categoría, comercio, contraparte, moneda y etiquetas. Desde un resultado se accede al detalle y sus relaciones.

Corregir categoría o nota no equivale a cambiar importe, fecha o cuenta. Las modificaciones financieras muestran consecuencias y actualizan todos los efectos relacionados. Una operación compuesta no se corrige parcialmente dejando posiciones inconsistentes.

La anulación de un pago actualiza su deuda y vencimientos. Si se modifica historia que afecta un saldo reconciliado, se advierte y se identifica la necesidad de revisión. Se conserva el historial relevante, no una cifra inexplicable.

## 10. Transferencias y reconciliación

### 10.1. Transferencias entre cuentas propias

Se indica origen, destino, fecha, importe y, si corresponde, importe recibido y comisiones.

- Las posiciones deben ser distintas y del mismo espacio.
- La persona debe poder operar ambos extremos.
- Los efectos se confirman juntos o no se registra la transferencia.
- Un reintento no crea otra operación.
- Las comisiones son gastos vinculados, no parte invisible de la transferencia.
- Desde cada cuenta se identifica la operación, respetando los permisos actuales sobre el otro extremo.

No se considera transferencia interna una operación entre espacios diferentes. Su tratamiento no debe improvisarse como gasto o ingreso para ocultar la limitación: la representación de aportes o movimientos entre espacios requiere una definición específica si se incorpora.

### 10.2. Reconciliación

El usuario compara el saldo calculado con el observado en una fecha, revisa la diferencia y corrige operaciones conocidas. Si permanece una diferencia sin explicación, puede registrar un ajuste con motivo y referencia temporal.

Ejemplo: el sistema muestra S/ 1.200 y la cuenta real S/ 1.150. Un ajuste de S/ -50 permite reflejar la posición observada, pero deja visible que hay S/ 50 sin explicación financiera. No inventa una compra ni presenta el historial como completo.

La reconciliación conserva saldo previo, saldo observado, diferencia, fecha, autor y motivo. Ajustar hoy no debe modificar silenciosamente el saldo inicial de meses anteriores. Las nuevas correcciones o reconciliaciones mantienen trazabilidad.

## 11. Clasificación y contrapartes

### 11.1. Categorías y subcategorías

- Categorías de ingreso y gasto, con nombre, icono y color.
- Máximo dos niveles: categoría y subcategoría.
- Conjunto inicial breve, editable y adecuado al contexto regional.
- Creación, modificación, fusión y archivo, explicando el efecto sobre historia y presupuestos.
- Resúmenes por categoría principal y acceso al detalle de subcategorías.
- Identidad de categorías predeterminadas independiente de su traducción.

Transferencias internas y devolución de capital no requieren categorías de consumo. Los intereses y las comisiones sí tienen clasificación de gasto. La falta de categoría no impide registrar un hecho conocido, pero permanece visible como tarea de revisión.

### 11.2. Comercios, contrapartes y etiquetas

- El comercio responde dónde se compró; la contraparte identifica con quién existe una operación o acuerdo.
- Se pueden registrar y reutilizar personas, comercios, bancos y empresas, evitando variantes innecesarias de un mismo nombre.
- Se permite consolidar duplicados sin perder movimientos relacionados.
- Las etiquetas son opcionales y transversales, por ejemplo viaje, trabajo o proyecto.
- Los nombres y relaciones operativos pertenecen al espacio; no se divulgan entre usuarios de espacios diferentes.

No es necesario un catálogo global de marcas para operar. Si se incorpora uno, será una fuente opcional de sugerencias y no reemplazará el control del usuario sobre sus comercios.

## 12. Deudas y préstamos recibidos

### 12.1. Base común

Toda deuda identifica contraparte, origen, moneda, fechas conocidas, monto original si se conoce, pendiente a una fecha, pagos, próximos vencimientos, condiciones, notas y documentos.

Se diferencia deuda simple de préstamo estructurado. Desconocer tasa, monto original o distribución no equivale a registrar cero. Puede ampliarse un acuerdo simple con condiciones estructuradas conservando su identidad e historia.

### 12.2. Deudas simples

Permiten registrar dinero prestado por familiares, amigos o entidades y otras obligaciones sencillas sin exigir tasa, plazo o cuota fija.

Incluyen pagos parciales, vencimiento opcional, plan de pagos opcional, nuevos desembolsos identificados, condonación y cambios de condiciones.

### 12.3. Préstamos estructurados

Incluyen las condiciones conocidas de capital original, capital pendiente, intereses, cargos, seguros, tasa, plazo, calendario, cuotas y pagos anticipados.

Se prioriza el calendario o estado proporcionado por la entidad. No se presume que una tasa y un monto permiten reconstruir cualquier contrato. No se calculan condiciones desconocidas como si fueran confirmadas.

Se muestran por separado:

- Calendario acordado: qué se debería pagar.
- Pagos reales: qué se pagó y cómo se aplicó.
- Proyección: qué podría ocurrir según información y supuestos disponibles.

El paso del tiempo no reduce la deuda ni convierte una cuota esperada en pagada.

### 12.4. Pagos y distribución

El usuario registra fecha, importe, cuenta de salida y distribución conocida. Un pago de S/ 500 puede representar S/ 420 de capital, S/ 60 de intereses y S/ 20 de seguro.

- La salida de efectivo es S/ 500.
- La reducción de capital es S/ 420.
- Los costos son S/ 80, sin repetir los ya reconocidos.

Si falta el desglose, se registra el total y su aplicación conocida, dejando pendiente la distribución. No se atribuye todo al capital ni se presenta su saldo como preciso sin evidencia. El saldo total confirmado y el capital identificado se distinguen.

Se permite cubrir parte de una cuota, varias cuotas o un abono extraordinario. Un pago no puede aplicarse por un importe superior al realmente registrado. Excedentes y diferencias se identifican para asignación explícita, no se pierden ni se distribuyen por adivinación.

### 12.5. Casos y estados

- Pagos parciales, tardíos y correcciones.
- Adelanto de cuotas y abono a capital como acciones diferentes, según el acuerdo.
- Nuevos desembolsos y cargos, conservando el origen del aumento de deuda.
- Condonación parcial o total.
- Refinanciamiento con vínculo al acuerdo anterior, sin simular una salida de efectivo inexistente.
- Deuda activa, liquidada, condonada, refinanciada o en disputa.
- Atraso derivado de vencimientos pendientes, no de una etiqueta mantenida manualmente.

El detalle muestra pendiente, fecha de referencia, vencidos, próximo pago, pagos realizados, capital amortizado conocido, costos, calendario y datos faltantes. No se muestra progreso de amortización si no puede calcularse con información suficiente.

## 13. Derechos de cobro y dinero prestado

Se admiten contrapartes personales, bancarias y empresariales. El significado depende del origen del derecho, no de quién debe pagar.

Orígenes contemplados:

- Capital prestado.
- Gastos adelantados y recuperables.
- Servicios pendientes de cobro.
- Devoluciones pendientes de una empresa.

El usuario registra acuerdo, origen, moneda, importe, fecha, condiciones y cobros esperados. Puede registrar desembolsos y cobros parciales, indicar la cuenta de recepción, aplicar cada cobro y consultar el saldo restante.

Reglas:

- Recuperar capital prestado o un adelanto no genera ingreso ganado.
- Un cobro por servicios reconoce ingreso cuando corresponde y no debe duplicar un ingreso ya registrado. Se conserva el vínculo al registro previo si existe; crear una expectativa de cobro no equivale a recibir dinero.
- Los intereses se identifican por separado y se reconocen una sola vez.
- Una fecha esperada no garantiza recuperación ni aumenta efectivo.
- Condonar o dar por perdido no equivale a cobrar. El importe se identifica en el historial y análisis de pérdidas, sin fingir movimiento de efectivo.
- Si la misma contraparte debe dinero y también es acreedora, ambas posiciones permanecen separadas. Una compensación acordada requiere registro explícito.
- Los recordatorios son para los usuarios autorizados del producto, no mensajes de cobranza enviados a terceros.

## 14. Tarjetas de crédito

### 14.1. Información y ciclos

Por moneda se consulta deuda actual, saldo a favor, límite, crédito disponible cuando se conoce, fecha de cierre, vencimiento, saldo del estado y pago mínimo.

La deuda actual, el pendiente del último estado y el pago mínimo son cifras diferentes. Las compras posteriores al cierre pueden aumentar la deuda sin pertenecer al estado que vence próximamente.

Cada ciclo permite consultar período, compras, cargos, devoluciones, saldo del estado, pagos aplicados, pendiente y documento asociado. Una reconstrucción no confirmada por un estado se identifica como estimación.

### 14.2. Compras, cuotas y pagos

- La compra registra gasto y aumenta deuda.
- El pago manual reduce dinero y deuda sin repetir el gasto.
- Se admiten pagos completos y parciales, mostrando qué obligación cubren.
- Las compras en cuotas registran el gasto original una sola vez y distribuyen su exigibilidad futura.
- Intereses y cargos se registran aparte.
- Se distinguen pendiente total, exigible en el próximo ciclo y compromisos posteriores.
- La asignación del banco entre conceptos no se inventa cuando no se conoce.

Una tarjeta con varias monedas agrupa posiciones separadas. Las devoluciones y sobrepagos pueden generar saldo a favor, que se distingue del efectivo y del crédito disponible. Ninguna de estas cifras se suma automáticamente al dinero líquido.

## 15. Recurrentes, suscripciones y agenda

### 15.1. Regla de funcionamiento manual

**El usuario configura los recurrentes. No existe detección automática y alcanzar una fecha no crea movimientos, descuenta saldos, reduce deudas, ejecuta pagos ni marca obligaciones como pagadas.**

Se pueden mostrar fechas previstas y enviar recordatorios según configuración. La programación y las estimaciones no son hechos financieros. Un cobro automático realizado por un proveedor fuera de la aplicación también requiere registro manual en el producto.

Este apartado describe compromisos financieros registrados por el usuario. La contratación comercial del propio espacio se define en el apartado 6.9 y permanece separada de estos movimientos.

### 15.2. Configuración de series

Se pueden programar suscripciones, servicios, alquileres, seguros, ingresos esperados y otros compromisos repetidos. También existen pagos y cobros puntuales futuros.

Una serie incluye nombre, contraparte opcional, moneda, cuenta o medio previsto, clasificación sugerida, fecha inicial, frecuencia, fin opcional, número de repeticiones y recordatorios.

Frecuencias contempladas:

- Semanal.
- Cada dos semanas.
- Mensual.
- Dos fechas mensuales.
- Anual.
- Cada un número definido de semanas o meses.

Cada dos semanas y dos veces al mes no son equivalentes. Para días 29, 30 o 31 inexistentes se utiliza el último día disponible, conservando el día elegido para los meses posteriores. El usuario puede revisar las fechas resultantes.

El importe puede ser fijo, estimado, un rango o desconocido. El importe real se confirma al registrar el hecho o al conocer el monto exigible. Un rango no permite decidir por sí solo que una obligación está pagada.

### 15.3. Vencimientos y aplicaciones

Cada vencimiento mantiene su fecha, importe esperado o confirmado, pagos asociados y pendiente. Puede estar pendiente, próximo, vencido, parcialmente pagado, pagado, omitido o cancelado según corresponda.

Para registrar un pago:

1. El usuario abre el vencimiento.
2. Vincula un movimiento existente o registra uno nuevo.
3. Revisa cuenta, fecha e importe reales.
4. Confirma el importe aplicado.
5. El producto actualiza saldos y cumplimiento como consecuencia de esa acción.

Varias aplicaciones pueden cubrir una obligación y un movimiento puede cubrir varias, sin superar el importe disponible del movimiento. No se marca como totalmente pagada una obligación confirmada de S/ 2.000 que solo recibió S/ 1.850.

Si el monto era estimado, el usuario puede confirmar el real y cerrar el vencimiento con esa evidencia. No se cierra automáticamente por cercanía de importes. Anular o desvincular un pago obliga a recalcular su cumplimiento.

Para operaciones dentro del seguimiento, una declaración de pago sin movimiento asociado se considera incompleta y no cambia saldos ni se presenta como registro financiero completo. Los antecedentes anteriores al inicio tienen el tratamiento histórico definido en la incorporación de datos.

### 15.4. Cambios y recordatorios

- Cambiar frecuencia o precio no reescribe vencimientos históricos confirmados ni pagos realizados.
- Pausar, finalizar o cancelar una serie conserva sus antecedentes y obligaciones ya existentes.
- Omitir una ocurrencia no representa un pago.
- Posponer un recordatorio no cambia la fecha real de vencimiento.
- Renegociar una fecha es una modificación explícita del compromiso.
- Los recordatorios son configurables y respetan permisos y privacidad.
- En un espacio compartido se evita que dos integrantes registren el mismo pago por un reintento de la misma acción; las posibles duplicaciones de registros distintos requieren revisión, no eliminación automática.

### 15.5. Suscripciones

Además del comportamiento recurrente, incluyen plan, prueba gratuita, renovación, límite para cancelar, cambios de precio, último cargo y cancelación registrada.

El costo mensual o anual equivalente permite comparar compromisos, pero no se presenta como dinero ya gastado. Cancelar el seguimiento dentro del producto no cancela el contrato con el proveedor.

### 15.6. Agenda unificada

Reúne vencimientos puntuales, recurrentes, cuotas y tarjetas, con filtros de próximos, vencidos, parciales y completados. Los vencimientos de una deuda o tarjeta se muestran desde su origen; no se recrean como series independientes.

Una suscripción con tarjeta aumenta deuda cuando se registra el cargo. Su posterior pago afecta efectivo. La proyección no descuenta ambos como dos salidas de la misma cuenta bancaria.

## 16. Presupuestos, reservas y proyecciones

### 16.1. Presupuestos

Se define un importe de gasto por categoría principal y período. Se muestra planificado, consumido neto, restante, exceso, evolución y movimientos que explican el resultado.

- La compra con tarjeta consume presupuesto cuando ocurre; pagarla no vuelve a consumirlo.
- Transferencias y amortización de capital no se convierten en consumo.
- Intereses y cargos sí son gastos.
- Excepcionales se incluyen por defecto y pueden tener planificación específica.
- Las devoluciones reducen gasto neto con su fecha registrada.
- Se respeta el período operativo configurado.

El presupuesto no es una cuenta ni determina cuánto efectivo hay. Los usuarios lectores no pueden modificarlo; las acciones sobre planificación compartida respetan las facultades del espacio y no otorgan acceso a cuentas privadas.

### 16.2. Reservas y disponibilidad

Una reserva aparta conceptualmente dinero para una finalidad sin moverlo entre cuentas. Se distingue:

- **Dinero líquido registrado:** efectivo y saldos propios de cuentas líquidas.
- **Dinero libre según compromisos:** fondos disponibles después de las reservas y obligaciones seleccionadas para cubrir con ellos.
- **Saldo proyectado:** dinero actual más entradas esperadas menos salidas esperadas hasta una fecha.

Se explica la composición y se evita descontar dos veces una obligación ya cubierta por una reserva. Tener dinero visible de un grupo no equivale a disponer personalmente de él.

### 16.3. Proyección de efectivo

Se contemplan horizontes de 7, 30 y 90 días, con detalle por cuenta y moneda cuando sea necesario.

Se distinguen compromisos confirmados, importes estimados, ingresos esperados, cobros inciertos, reservas y consumo variable estimado. La vista conservadora no utiliza cobros inciertos para ocultar faltantes.

La proyección indica fechas de posible insuficiencia, datos faltantes y supuestos. No crea transacciones, no garantiza ingresos y no recomienda un importe seguro para gastar si la información es insuficiente. Un mismo importe puede ser gasto y obligación futura sin descontarse dos veces del efectivo.

## 17. Multimoneda

Cada espacio tiene moneda base y cada posición conserva su moneda propia. La moneda original no se pierde por consolidar información.

En una compra internacional pueden existir tres importes distintos:

- Precio original del comercio.
- Importe realmente cargado en la cuenta.
- Equivalente informativo en moneda base.

El saldo cambia por el importe realmente aplicado a la cuenta. Por ejemplo, una compra de USD 20 debitada por S/ 76 reduce esa cuenta en S/ 76, no en una conversión estimada diferente.

Reglas:

- Transferencias entre monedas conservan importe enviado y recibido; las comisiones se registran aparte.
- La tasa identifica dirección, fecha y origen, incluyendo una referencia manual cuando corresponda.
- Solo una tasa positiva y válida permite convertir monedas diferentes; nunca se sustituye una tasa faltante por 1.
- Los ingresos y gastos históricos utilizan referencias correspondientes al hecho registrado; los balances actuales utilizan referencias de valoración actual identificadas.
- Se conserva la referencia utilizada para poder explicar conversiones. Una corrección posterior debe ser trazable, no reescribir resultados silenciosamente.
- La valoración cambiaria no se confunde con ingreso o gasto operativo.
- Los reportes indican qué importes están convertidos y cuáles son originales.
- Si faltan tasas se muestra el desglose por moneda y un consolidado incompleto, no un total falsamente preciso.
- Cambiar la moneda base cambia la presentación, no los importes originales. Se explican los recálculos; las exportaciones conservan su contexto.

Las necesidades se revisan también por moneda: tener un equivalente suficiente en soles no significa tener dólares disponibles para un vencimiento en dólares.

## 18. Reportes y calidad de datos

### 18.1. Reportes incluidos

| Reporte | Contenido |
| --- | --- |
| Posición actual | Dinero por cuenta y moneda, obligaciones y derechos de cobro, separados. |
| Ingresos y gastos | Totales, resultado y evolución por período. |
| Efectivo | Entradas, salidas y evolución de liquidez, diferenciando financiamiento y movimientos internos. |
| Gasto | Categorías, subcategorías, comercios, etiquetas, devoluciones y excepcionales. |
| Deudas | Pendientes, capital conocido, pagos, costos, vencimientos y atrasos. |
| Derechos de cobro | Origen, contraparte, importes otorgados, recuperados, pendientes y pérdidas identificadas. |
| Recurrentes y suscripciones | Compromisos, renovaciones, equivalencias y cambios de precio. |
| Presupuesto | Plan frente a consumo y desviaciones. |
| Proyección | Evolución esperada, necesidades y posibles faltantes. |
| Calidad de datos | Información incompleta o diferencias que requieren revisión. |

### 18.2. Capacidades y reglas comunes

- Períodos calendario, operativos y personalizados.
- Filtros por cuenta, categoría, moneda, contraparte, comercio y etiquetas según el reporte.
- Desglose desde cada cifra hasta sus movimientos o supuestos.
- Comparaciones equivalentes y tendencias mensuales.
- Presentación de importes originales y consolidados.
- Fecha de actualización, fuentes y exclusiones relevantes.
- Exportación de resultados y registros con el mismo contexto de permisos.

Un período incompleto se compara por defecto con el mismo tramo del anterior. Si la base anterior es cero, no se inventa un porcentaje; si falta historial, se informa. Los filtros de un gráfico y su detalle coinciden.

Los reportes de un usuario solo incluyen información autorizada y deben indicar si representan un subconjunto del espacio. El encabezado no puede prometer un total familiar completo si excluye cuentas privadas. Las cuentas archivadas conservan su historia.

La evolución consolidada de saldos se explica mediante saldo inicial, entradas, salidas, ajustes y efecto de valoración cambiaria cuando corresponda. Este último se muestra por separado y no como sueldo o compra.

### 18.3. Información pendiente de revisión

Se identifica:

- Movimientos sin categoría.
- Posibles duplicados.
- Pagos sin desglose o aplicación suficiente.
- Tasas de cambio faltantes.
- Ajustes sin explicación financiera.
- Cuentas sin reconciliación reciente.
- Historial parcial.
- Vencimientos sin importe confirmado.
- Declaraciones de pagos incompletas.

Cada aviso permite consultar el origen y corregirlo. Un saldo reconciliado no oculta que el detalle de gastos puede seguir incompleto.

## 19. Documentos e incorporación de información

### 19.1. Documentos financieros

Se pueden guardar recibos, contratos, estados de cuenta y otros archivos de contexto. Pertenecen al espacio y pueden relacionarse con cuentas, operaciones, acuerdos o períodos, sin depender exclusivamente de un movimiento.

Incluyen nombre, tipo, fecha o período cuando se conozca, nota y relaciones pertinentes. Se permite listarlos y localizarlos por esos datos. Sus permisos no son más amplios que los del contenido financiero relacionado. Subir un documento no modifica saldos ni habilita automáticamente su envío a la IA.

### 19.2. Incorporación guiada y antecedentes

El usuario puede comenzar sin reconstruir toda su historia:

1. Establece una fecha inicial de seguimiento por cuenta o posición.
2. Registra el saldo al comienzo de esa fecha.
3. Incorpora deudas, tarjetas y derechos de cobro pendientes.
4. Agrega antecedentes, documentos y pagos históricos conocidos.
5. Programa compromisos e ingresos esperados.
6. Revisa diferencias e información faltante.

Los antecedentes ya incluidos en el saldo inicial no vuelven a aplicarse a las cuentas actuales. Se identifican como historia previa y no como nuevos movimientos efectivos.

Ejemplo: una deuda original de S/ 10.000 con pendiente de S/ 6.000 al inicio puede conservar pagos antiguos sin volver a descontarlos del banco. Si el original es desconocido, se conserva el pendiente sin inventar capital amortizado.

Se señala dónde comienza el historial confiable. Incorporar un saldo inicial no se presenta como ingreso o gasto del período.

### 19.3. Importación de archivos tabulares

Se incluye carga de archivos tabulares, como CSV, con:

- Selección del espacio y cuentas autorizadas.
- Mapeo de columnas, moneda y sentido de entradas y salidas.
- Interpretación explícita de fechas y separadores numéricos.
- Vista previa y errores por registro.
- Revisión de posibles duplicados sin eliminaciones automáticas por similitud.
- Confirmación antes de afectar información financiera.
- Identificación de los registros por lote importado y estado del proceso.
- Recuperación ante interrupciones sin informar como fallida una incorporación ya realizada.
- Reversión controlada, mostrando cambios o relaciones posteriores que deban resolverse.

Importar no debe omitir silenciosamente registros problemáticos ni incorporar antecedentes como si fueran movimientos posteriores al saldo inicial.

## 20. Asistente de inteligencia artificial

### 20.1. Consultar y explicar

El asistente puede consultar información vigente y autorizada sobre cuentas, movimientos, gastos, deudas, cobros, presupuestos y compromisos. Por ejemplo:

- Cuánto se gastó en alimentación durante un período.
- Cuánto queda por pagar a una contraparte.
- Qué obligaciones vencen antes del siguiente ingreso esperado.
- Cuánto representan las suscripciones al año.
- Por qué una categoría o deuda cambió.

Las respuestas incluyen período, moneda, alcance, evidencia navegable y advertencias relevantes. Los cálculos los proporciona el producto; el modelo no reconstruye saldos mediante suposiciones ni usa mensajes antiguos como fuente vigente.

### 20.2. Orientación financiera

Puede explicar hábitos y costos, comparar períodos y señalar compromisos o información que conviene revisar. Distingue hechos, interpretaciones y alternativas, sin prometer resultados ni presentarse como asesor acreditado.

No recomienda comprar o vender productos financieros concretos. Si faltan condiciones, historial o datos, lo informa en lugar de emitir conclusiones seguras. Las simulaciones avanzadas de amortización y estrategias de deuda no forman parte del alcance definido.

### 20.3. Preparar acciones confirmables

El usuario puede describir un gasto, ingreso, compromiso o corrección para recibir una propuesta estructurada. El flujo es:

1. Interpretar la solicitud y consultar lo necesario.
2. Pedir datos esenciales faltantes, sin adivinar cuenta, moneda o contraparte.
3. Mostrar la acción propuesta y sus efectos.
4. Recibir confirmación explícita del usuario autorizado.
5. Validar permisos y datos vigentes.
6. Ejecutar por las mismas reglas que la operación manual.
7. Informar el resultado real y permitir consultarlo.

La confirmación es un requisito efectivo del producto, no solo una instrucción al modelo. Si cambió información relevante, se vuelve a revisar la propuesta. Un reintento no repite una operación y un fallo no se comunica como éxito.

### 20.4. Conversaciones, contexto y privacidad

- Las conversaciones persisten para su consulta y pueden eliminarse según la política informada.
- Están vinculadas a usuario y espacio; no son conversaciones familiares visibles por defecto.
- Cambiar de espacio no arrastra información privada del anterior.
- La IA es opcional; las funciones financieras no dependen de su disponibilidad.
- Se solicita consentimiento informado y se minimizan los datos enviados a proveedores.
- No se envían credenciales ni números completos de cuentas o tarjetas.
- Se utilizan seudónimos o referencias reducidas cuando sea posible.
- No se promete anonimato absoluto: mensajes, nombres y documentos pueden contener datos personales.
- Retención, proveedores y tratamiento deben explicarse con claridad.
- Si se incorpora lectura de documentos mediante IA, requerirá consentimiento y permisos adicionales; guardar un archivo no autoriza analizarlo externamente.
- Los archivos y descripciones son contenido no confiable, no instrucciones para ejecutar acciones.

El asistente no realiza pagos bancarios, no modifica información por iniciativa propia, no evita permisos y no aplica automáticamente recurrentes.

## 21. Experiencia Android y flujos principales

### 21.1. Organización

Las áreas funcionales son inicio, movimientos, cuentas, deudas y cobros, agenda, presupuestos, reportes, asistente y configuración. Su distribución visual se definirá para una navegación móvil simple; no implica mostrar todas como pestañas.

Desde el contexto del espacio se consulta su plan, capacidades y límites. Las acciones de contratación y facturación solo se muestran como operativas a quienes estén autorizados; no se confunden con la lista de suscripciones financieras personales.

El inicio prioriza dinero líquido y actualización, compromisos próximos o atrasados, situación del período y datos que necesitan revisión. Los documentos aparecen junto a su contexto y mediante un listado común cuando se necesite localizarlos.

### 21.2. Flujos cotidianos

- **Gasto:** indicar importe, confirmar cuenta y fecha, clasificar o dejar pendiente y guardar. Mostrar efecto y confirmación.
- **Pago de deuda:** elegir acuerdo o vencimiento, cuenta, importe y distribución conocida; confirmar y mostrar saldo y pendientes.
- **Cobro:** elegir derecho, cuenta receptora e importe, confirmar aplicación y mostrar restante.
- **Recurrente pagado:** abrir vencimiento, crear o vincular movimiento, confirmar el importe real y mostrar cumplimiento.
- **Reconciliación:** comparar saldos, revisar diferencia, corregir hechos conocidos o registrar ajuste identificado.
- **Invitación:** revisar espacio y rol, aceptar y acceder sin modificar otras pertenencias.
- **Compartir un espacio individual:** invitar personas al espacio existente y revisar qué cuentas tendrán disponibles, sin migrar datos ni cambiar automáticamente su contratación.
- **Contratación:** consultar el plan del espacio, revisar condiciones y confirmar un cambio autorizado, sin modificar movimientos financieros ni otros espacios.

### 21.3. Calidad de interacción

- Captura breve con campos adicionales cuando sean necesarios.
- Uso con una mano y controles accesibles.
- Estados expresados con texto, no únicamente color.
- Importes siempre acompañados de moneda cuando exista ambigüedad.
- Gráficos con información legible alternativa.
- Errores que preservan lo ingresado y explican cómo continuar.
- Confirmaciones que distinguen operación guardada, en proceso y no realizada.
- Estados vacíos que orientan sin hacer pasar ejemplos por datos reales.
- Notificaciones discretas por defecto, configurables sin exponer importes sensibles innecesariamente.
- Lenguaje claro, sin culpabilizar a la persona por deudas o hábitos.

## 22. Seguridad, confiabilidad y operación del servicio

### 22.1. Protección y permisos

- Aislamiento entre espacios y verificación de membresía vigente en cada acción.
- Aplicación consistente de permisos a información original, agregados y archivos.
- Revocación de acceso al retirar integrantes, también en tareas pendientes e IA.
- Revisión de permisos actuales al abrir notificaciones, documentos o resultados antiguos.
- Confirmación adicional para acciones sensibles.
- Acceso de soporte restringido, justificado y auditado; sin lectura financiera irrestricta por defecto.
- Protección de datos transmitidos y almacenados.
- Registros operativos que eviten exposición innecesaria de información financiera.

### 22.2. Confiabilidad financiera

- Precisión monetaria y redondeo explícito según la moneda; sin importes alterados silenciosamente.
- Una operación compuesta queda completa o no se confirma.
- Reintentos seguros y control de ediciones simultáneas.
- Imposibilidad de aplicar un mismo importe por encima de su capacidad mediante pagos concurrentes.
- Cálculos actualizados o señalados como pendientes, nunca saldos antiguos presentados como ya actualizados.
- Fallos de IA, importación o notificaciones sin corrupción de registros confirmados.
- Respaldos protegidos y recuperación comprobada.
- Trazabilidad de correcciones sin necesidad de mantener varias verdades financieras.

### 22.3. Datos, soporte y condiciones públicas

El producto contempla exportación, solicitudes de eliminación, información de retención, condiciones de uso, privacidad, gestión de incidentes y límites claros de archivos, importaciones y uso de IA. Las condiciones concretas deben definirse antes de ofrecer el servicio al público, con revisión jurídica cuando corresponda.

Eliminar una identidad no debe borrar por accidente información compartida perteneciente a otros integrantes. Se deben resolver responsabilidades y conservación de historia, minimizando los datos personales según las obligaciones aplicables.

No se promete eliminación instantánea de todos los respaldos ni de datos retenidos por terceros. El alcance y los tiempos se explican al usuario.

La contratación por espacio forma parte del producto y se define en el apartado 6.9. Está separada de las suscripciones financieras registradas por el usuario. Este documento fija su unidad de contratación y reglas de separación; no fija precios, catálogo de planes, límites concretos ni proveedor de cobro.

### 22.4. Administración interna de la plataforma

#### 22.4.1. Identidades y acceso separados

El personal utiliza identidades y sesiones internas diferentes de sus cuentas de cliente. Una misma persona puede utilizar el producto y trabajar en su administración, pero sus accesos y atribuciones no se mezclan.

- No existe registro público como administrador interno.
- Crear un espacio, ser su propietario, recibir una invitación o contratar un plan no otorga acceso a la plataforma administrativa.
- Los accesos del personal son individuales, con segundo factor obligatorio y revocación independiente de sus membresías como cliente.
- No se comparten credenciales administrativas.
- La asignación y retirada de facultades internas son acciones restringidas y auditadas.

#### 22.4.2. Funciones y permisos internos

El ámbito administrativo permite, según las facultades de cada persona:

- Localizar usuarios y espacios y consultar sus estados administrativos.
- Revisar membresías, invitaciones y problemas de acceso.
- Gestionar planes, suscripciones, facturación, límites y consumo.
- Revisar incidencias y procesos fallidos sin exponer innecesariamente contenido financiero.
- Revocar sesiones o suspender accesos con motivo registrado.
- Tramitar solicitudes de soporte, privacidad y eliminación.
- Consultar auditoría y realizar las acciones administrativas expresamente autorizadas.

Se separan permisos de soporte, facturación y administración sensible. No es necesario que los desempeñen personas distintas, pero reunir varias funciones no elimina sus controles. La suspensión de una identidad por seguridad es diferente de una restricción comercial sobre un espacio; ninguna de ellas implica cancelar automáticamente contrataciones de otros integrantes.

#### 22.4.3. Información administrativa y contenido financiero

Administrar clientes no permite recorrer libremente sus movimientos, saldos, contratos o conversaciones. La información administrativa necesaria para gestionar el servicio y el contenido financiero sensible tienen autorizaciones diferentes.

Consultar datos administrativos de varios espacios no convierte al personal en miembro financiero de ellos ni le concede capacidad para operar sus cuentas. La facturación tampoco requiere acceso a documentos financieros personales o conversaciones de IA.

#### 22.4.4. Acceso de soporte controlado

Cuando un caso requiera contenido financiero, se debe establecer:

- Caso y motivo identificados.
- Autorización correspondiente antes del acceso.
- Espacio y datos estrictamente necesarios.
- Duración definida y revocación al finalizar, vencer o retirarse la autorización.
- Consulta como modalidad predeterminada, sin capacidad de edición financiera por defecto.
- Identificación del operador real, sin ocultar su intervención como si fuera una acción del cliente.

Las operaciones sensibles requieren confirmación adicional y, cuando corresponda, otra aprobación. Una autorización de consulta no permite exportar o modificar datos por extensión. La necesidad de una operación adicional debe validarse expresamente.

La auditoría registra quién accedió, a qué ámbito, con qué motivo, cuándo y qué consultas sensibles, exportaciones o cambios realizó. Los registros deben estar protegidos de alteraciones no autorizadas y no convertirse en copias innecesarias de contenido financiero.

Los perfiles internos concretos, plazos, mecanismos de autorización y acciones que requieren otra aprobación deben detallarse posteriormente como decisiones de producto y seguridad, sin asumir acceso global irrestricto durante su definición.

## 23. Límites del alcance y posibilidades de ampliación

### 23.1. No forman parte del alcance definido

- Conexiones bancarias y sincronización automática de movimientos.
- Detección automática de recurrentes y vinculación automática de sus pagos.
- Creación de movimientos por alcanzar fechas o por estimaciones.
- Ejecución de pagos o transferencias bancarias.
- Otorgamiento de crédito o cobranza automatizada a terceros.
- Uso sin conexión.
- Clientes financieros web, escritorio o iOS como entregables definidos. Esto no excluye el ámbito de administración interna, cuya interfaz no está elegida.
- Extracción automática de datos desde imágenes o documentos.
- Búsqueda semántica y respuestas de IA sobre contenido documental.
- Simulaciones avanzadas de amortización, tasas y estrategias de deuda.
- Reparto automático de gastos y compensaciones grupales.
- Consolidación de todos los espacios del usuario.
- Inversiones, criptoactivos y análisis patrimonial avanzado.
- Contabilidad empresarial, emisión de facturas del usuario a sus clientes y declaraciones tributarias. La facturación de nuestro propio servicio por espacio sí forma parte del alcance.
- Plataforma pública de integraciones, API para terceros, claves de acceso externas o MCP.

### 23.2. Posibilidades no comprometidas

Podrán evaluarse otros clientes, captura sin conexión, conexiones bancarias, lectura asistida de comprobantes, consultas documentales con IA, simulaciones avanzadas, objetivos de ahorro, vistas guardadas, resúmenes periódicos y acceso para integraciones.

Son posibilidades, no una secuencia de desarrollo ni compromisos implícitos. Cualquier incorporación requiere valorar utilidad, esfuerzo, seguridad y efecto sobre las reglas existentes. La expansión internacional y las condiciones comerciales concretas también requieren decisiones propias; la contratación por espacio y la separación de accesos internos ya forman parte del diseño definido.

## 24. Casos que verifican el comportamiento del producto

Estos ejemplos definen resultados esperados de negocio; no constituyen una guía técnica de pruebas.

| Caso | Resultado esperado |
| --- | --- |
| Un usuario acepta una invitación familiar. | Conserva su espacio personal y demás membresías; no se trasladan cuentas. |
| Se acepta una invitación a un espacio que comenzó como personal. | Se comparte el mismo espacio sin migrar datos, crear otro ni cambiar automáticamente su contratación. |
| Se acepta la primera invitación de un espacio individual. | Las cuentas privadas existentes no se vuelven compartidas por ese hecho. |
| Un usuario participa en varios espacios con planes diferentes. | Cada espacio conserva sus capacidades, límites y contratación independientes. |
| Un integrante utiliza un espacio con plan de pago. | No necesita contratar otra suscripción individual para participar; utiliza capacidades según sus permisos. |
| Se intenta utilizar el plan de una familia en otro espacio. | No se trasladan beneficios ni se mezclan consumos entre espacios. |
| El plan deja de permitir nuevos integrantes antes de aceptar una invitación. | Se vuelve a validar y se explica la restricción; no se crea una membresía parcial ni se alteran otras pertenencias. |
| Se transfiere la propiedad de un espacio. | No cambia silenciosamente el responsable de facturación ni se utiliza otro medio de pago sin autorización. |
| Se repite una confirmación de contratación. | No se crea otra contratación ni se aplica dos veces el mismo cambio. |
| Falla el pago comercial de un espacio. | No se bloquean por ese motivo los demás espacios del usuario. |
| Se cancela o reduce el plan de un espacio. | No se borran automáticamente datos ni se eliminan integrantes; se aplican condiciones explícitas. |
| Se renueva comercialmente el servicio del espacio. | No se crea automáticamente un gasto ni se descuenta saldo en sus cuentas financieras. |
| Un lector recibe acceso a una cuenta. | Puede consultar, no modificar operaciones. |
| Un administrador consulta una cuenta privada no compartida. | No obtiene su información por el hecho de administrar miembros. |
| Un propietario de espacio intenta acceder a la administración interna. | Su rol financiero no concede identidad, sesión ni permisos internos. |
| Personal de facturación consulta el estado comercial de un espacio. | No obtiene por ello movimientos, documentos financieros ni conversaciones. |
| Soporte necesita consultar contenido financiero. | Requiere caso, motivo, autorización, alcance y duración definidos; el acceso queda auditado. |
| Vence o se retira una autorización de soporte. | El acceso autorizado termina y no puede seguir utilizándose la sesión para consultar esos datos. |
| Se retira a una persona de un grupo. | Pierde acceso al grupo; conserva su usuario y otras pertenencias; permanece su autoría histórica. |
| Un reporte excluye cuentas privadas. | Indica su alcance; no se presenta como total completo del espacio. |
| Se transfieren S/ 500 del banco a efectivo. | Cambian ambas cuentas; no aumentan ingresos ni gastos. |
| Se compra S/ 120 con tarjeta y se paga después. | El gasto total es S/ 120, no S/ 240; el pago reduce efectivo y deuda. |
| Se paga una cuota de S/ 500 con S/ 420 de capital. | Sale S/ 500; capital baja S/ 420; los otros conceptos no se cuentan dos veces. |
| Se registra una cuota sin desglose. | Se conserva el pago conocido y se informa la distribución pendiente. |
| Se prestan S/ 300 y se recuperan S/ 100. | Quedan S/ 200 de capital por cobrar; recuperar capital no genera ingreso ordinario. |
| Se pagan S/ 1.850 de una obligación confirmada de S/ 2.000. | Quedan S/ 150 y el vencimiento sigue parcialmente pagado. |
| Llega el día de una suscripción. | Puede mostrarse el recordatorio; no cambian saldos ni se registra un pago. |
| Se registra manualmente el pago de esa suscripción. | Se crea o vincula un único movimiento y se actualiza el vencimiento tras confirmar. |
| Se modifica el precio de una serie. | No se reescribe historia confirmada ni pagos previos. |
| Se cancela una serie. | Se conserva su historia; no se afirma que el proveedor haya cancelado el servicio. |
| Se anula un pago. | Se recalculan saldo, deuda y vencimientos afectados. |
| Se ajusta una cuenta de S/ 1.200 a S/ 1.150 sin explicación. | No se inventa gasto; permanece una diferencia identificada de S/ -50. |
| Se marca una compra como excepcional. | Permanece en el gasto real; su exclusión solo afecta análisis expresamente identificados. |
| Falta una tasa de cambio. | No se usa equivalencia 1:1 ni se presenta un consolidado completo. |
| Se incorpora un pago histórico ya incluido en el saldo inicial. | No se vuelve a descontar del saldo actual. |
| Se archiva una cuenta. | Permanece en los reportes históricos correspondientes. |
| Se repite una solicitud de guardado tras una interrupción. | No se duplica la misma operación. |
| La IA responde con los mismos filtros y permisos que un reporte. | Las cifras coinciden y muestran el mismo alcance. |
| La IA prepara una modificación. | No se ejecuta sin confirmación y validación vigentes. |
| Se intenta abrir un documento sin acceso actual. | No se revela, aunque exista una notificación o referencia antigua. |
