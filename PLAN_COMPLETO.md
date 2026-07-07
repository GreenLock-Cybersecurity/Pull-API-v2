# Pull API v2 - Plan Completo de Desarrollo

> **Documento de referencia para continuar el desarrollo desde cualquier punto**
>
> Última actualización: 4 de Febrero 2026
> Estado actual: **Fase 2 - Infraestructura Base Completada**

---

## Tabla de Contenidos

1. [Visión General del Proyecto](#1-visión-general-del-proyecto)
2. [Arquitectura Multi-Tenant](#2-arquitectura-multi-tenant)
3. [Payment Gateways Per-Venue](#3-payment-gateways-per-venue)
4. [Schema de Base de Datos Actual](#4-schema-de-base-de-datos-actual)
5. [Migración: Single DB → Multi-Tenant](#5-migración-single-db--multi-tenant)
6. [Estado Actual del Proyecto](#6-estado-actual-del-proyecto)
7. [Estructura de Archivos](#7-estructura-de-archivos)
8. [Configuración del Entorno](#8-configuración-del-entorno)
9. [Fases de Desarrollo Detalladas](#9-fases-de-desarrollo-detalladas)
10. [APIs y Endpoints](#10-apis-y-endpoints)
11. [Guía de Implementación](#11-guía-de-implementación)
12. [Seguridad](#12-seguridad)
13. [Testing y Despliegue](#13-testing-y-despliegue)

---

## 1. Visión General del Proyecto

### 1.1 ¿Qué es Pull?

Pull es una **plataforma de venta de entradas para eventos** que opera como un servicio multi-tenant. La plataforma permite:

- **Venues (discotecas/locales)**: Crear eventos, vender entradas, gestionar reservas VIP
- **Usuarios públicos**: Comprar entradas, hacer reservas de grupo, gestionar sus tickets
- **Plataforma Pull**: Cobrar comisiones, gestionar todos los venues, ver analytics globales

### 1.2 Objetivo de Pull-API-v2

Crear una API **nueva desde cero** que reemplace a Pull-API-Go (mantenida como backup), con:

| Característica | Descripción |
|----------------|-------------|
| **Ultra-optimizada** | Connection pooling, buffer reuse, HTTP/2 |
| **Multi-tenant real** | Cada venue con su propia base de datos Supabase |
| **Múltiples pasarelas** | Stripe, NeoNet/Cybersource, MercadoPago por venue |
| **Comisiones configurables** | Fee % + Fee fijo por venue |
| **Código limpio** | Sin código legacy ni funciones no utilizadas |

### 1.3 Decisiones Arquitectónicas

| Decisión | Justificación |
|----------|---------------|
| Go (Gin framework) | Rendimiento, concurrencia nativa, tipado estático |
| Supabase por venue | Aislamiento de datos, escalabilidad independiente |
| Base de datos central | Registro de venues, configs, transacciones de plataforma |
| JWT para auth | Stateless, escalable, tres tipos de usuarios |
| AES-256-GCM | Encriptación de credenciales de pasarelas de pago |

---

## 2. Arquitectura Multi-Tenant

### 2.1 Diagrama de Arquitectura Completo

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              PULL API v2                                     │
│  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────────────────┐  │
│  │ DatabaseRouter  │  │ PaymentRouter   │  │ Auth Middleware             │  │
│  │                 │  │                 │  │ (Staff/User/Platform)       │  │
│  └────────┬────────┘  └────────┬────────┘  └─────────────────────────────┘  │
└───────────┼─────────────────────┼───────────────────────────────────────────┘
            │                     │
            ▼                     ▼
┌───────────────────────────────────────────────────────────────────────────────┐
│                         CENTRAL DATABASE (Pull Platform)                       │
│  ┌─────────────────────────────────────────────────────────────────────────┐  │
│  │  venues                    │  venue_database_configs                    │  │
│  │  ├─ id, name, slug         │  ├─ venue_id                               │  │
│  │  ├─ organization_id        │  ├─ supabase_url                           │  │
│  │  ├─ platform_fee_percent   │  ├─ service_key (encrypted)                │  │
│  │  ├─ platform_fee_fixed     │  └─ anon_key (encrypted)                   │  │
│  │  └─ payment_gateway        │                                            │  │
│  ├─────────────────────────────────────────────────────────────────────────┤  │
│  │  venue_payment_configs     │  platform_transactions                     │  │
│  │  ├─ venue_id               │  ├─ venue_id                               │  │
│  │  ├─ gateway (stripe/neonet)│  ├─ gross_amount                           │  │
│  │  ├─ credentials (encrypted)│  ├─ platform_fee_total                     │  │
│  │  └─ is_primary             │  ├─ venue_net_amount                       │  │
│  │                            │  └─ payment_gateway                        │  │
│  ├─────────────────────────────────────────────────────────────────────────┤  │
│  │  organizations             │  pull_staff (platform admins)              │  │
│  │  ├─ id, name, owner_id     │  ├─ email, password_hash                   │  │
│  │  └─ billing info           │  └─ role (admin/analyst/viewer)            │  │
│  └─────────────────────────────────────────────────────────────────────────┘  │
└───────────────────────────────────────────────────────────────────────────────┘
                                    │
        ┌───────────────────────────┼───────────────────────────┐
        ▼                           ▼                           ▼
┌───────────────────┐   ┌───────────────────┐   ┌───────────────────┐
│   VENUE A DB      │   │   VENUE B DB      │   │   VENUE C DB      │
│   (Supabase)      │   │   (Supabase)      │   │   (Supabase)      │
│                   │   │                   │   │                   │
│ - events          │   │ - events          │   │ - events          │
│ - ticket_types    │   │ - ticket_types    │   │ - ticket_types    │
│ - orders          │   │ - orders          │   │ - orders          │
│ - tickets         │   │ - tickets         │   │ - tickets         │
│ - group_reserv.   │   │ - group_reserv.   │   │ - group_reserv.   │
│ - vip_list_reserv.│   │ - vip_list_reserv.│   │ - vip_list_reserv.│
│ - public_users    │   │ - public_users    │   │ - public_users    │
│ - org_workers     │   │ - org_workers     │   │ - org_workers     │
│ - transactions    │   │ - transactions    │   │ - transactions    │
│ - vip_bottles     │   │ - vip_bottles     │   │ - vip_bottles     │
│ - guest_list_*    │   │ - guest_list_*    │   │ - guest_list_*    │
└───────────────────┘   └───────────────────┘   └───────────────────┘
```

### 2.2 Separación de Datos: Central vs Venue

#### Datos en CENTRAL DATABASE (Pull Platform)

| Tabla | Propósito | Notas |
|-------|-----------|-------|
| `organizations` | Empresas/grupos que tienen venues | Datos de facturación |
| `venues` | Registro de todos los venues | Solo metadata, fees |
| `venue_database_configs` | Conexión a DB de cada venue | Credenciales encriptadas |
| `venue_payment_configs` | Config de pasarela por venue | Credenciales encriptadas |
| `platform_transactions` | TODAS las transacciones de Pull | Para revenue reporting |
| `platform_daily_revenue` | Resumen diario de plataforma | Agregados |
| `platform_monthly_revenue` | Resumen mensual de plataforma | Agregados |
| `pull_staff` | Staff de la plataforma Pull | Admins, analysts |
| `pull_staff_sessions` | Sesiones de platform staff | Auth tokens |
| `support_requests` | Tickets de soporte | Cross-venue |

#### Datos en VENUE DATABASE (Por cada venue)

| Tabla | Propósito | Notas |
|-------|-----------|-------|
| `events` | Eventos del venue | Con ticket_types |
| `ticket_types` | Tipos de entrada por evento | Precios, cantidades |
| `event_vip_ticket_types` | Tickets VIP con precio por género | |
| `orders` | Compras de entradas individuales | Con payment info |
| `tickets` | Entradas generadas | Con QR code |
| `group_reservations` | Reservas de grupo/mesa | Con botellas |
| `group_reservation_guests` | Invitados de reserva de grupo | |
| `group_reservation_bottles` | Botellas de reserva de grupo | |
| `group_reservation_mixers` | Mixers de reserva de grupo | |
| `vip_list_reservations` | Reservas VIP list | Flow alternativo |
| `vip_list_guests` | Invitados VIP list | |
| `vip_list_bottles` | Botellas VIP list | |
| `vip_bottles` | Catálogo de botellas del venue | |
| `vip_mixers` | Catálogo de mixers del venue | |
| `guest_list_types` | Tipos de guest list | |
| `guest_list_signups` | Inscripciones a guest list | |
| `public_users` | Usuarios que han comprado | Datos de cliente |
| `organization_workers` | Staff del venue | Empleados |
| `roles` | Roles de staff | |
| `transactions` | Transacciones del venue | Detalle completo |
| `transaction_line_items` | Líneas de transacción | |
| `staff_notifications` | Notificaciones para staff | |
| `staff_push_tokens` | Push tokens de staff | |
| `daily_revenue_summary` | Resumen diario del venue | |
| `monthly_revenue_summary` | Resumen mensual del venue | |
| `event_revenue_summary` | Resumen por evento | |
| `verification_codes` | Códigos de verificación email | |
| `user_sessions` | Sesiones de usuarios | |
| `user_access_tokens` | Tokens de acceso | |
| `user_refresh_tokens` | Refresh tokens | |
| `user_venue_spending` | Gasto por usuario/venue | |
| `payment_transactions` | Transacciones de pago (NeoNet) | |
| `payment_audit_log` | Audit log de pagos | |

### 2.3 Tipos de Usuario y Autenticación

| Tipo | Token Claim | Base de Datos | Acceso |
|------|-------------|---------------|--------|
| **Staff** | `venue_id`, `staff_id`, `role` | Venue DB | Gestión del venue |
| **User** | `user_id`, `email` | Venue DB(s) | Comprar, ver tickets |
| **Platform** | `platform_staff_id`, `role` | Central DB | Admin de Pull |

### 2.4 Flujo de una Petición

```
1. Request llega
   └─► CORS middleware
       └─► Security Headers middleware
           └─► Rate Limiter middleware
               └─► Auth Middleware (extrae JWT)
                   │
                   ├─► Staff: venue_id del token → DB.ForVenue(venue_id)
                   ├─► User: venue_id del request → DB.ForVenue(venue_id)
                   └─► Platform: DB.Central()
                       │
                       ▼
                   Handler ejecuta operación
                       │
                       ├─► Si hay pago → PaymentRouter.ForVenue(venue_id)
                       │                 └─► Stripe / NeoNet / MercadoPago
                       │
                       └─► Si hay comisión → DB.Central().Insert("platform_transactions")
```

---

## 3. Payment Gateways Per-Venue

### 3.1 Arquitectura de Pasarelas de Pago

Cada venue puede tener su propia pasarela de pago configurada. El sistema soporta múltiples gateways:

```
┌─────────────────────────────────────────────────────────────────┐
│                   CENTRAL PULL DATABASE                          │
├─────────────────────────────────────────────────────────────────┤
│  venue_payment_configs                                           │
│  ├─ venue_id: UUID                                              │
│  ├─ gateway: "stripe" | "neonet" | "mercadopago"                │
│  ├─ is_primary: boolean                                         │
│  ├─ credentials: JSONB (encrypted)                              │
│  │   {                                                          │
│  │     "secret_key": "sk_...",                                  │
│  │     "publishable_key": "pk_...",                             │
│  │     "webhook_secret": "whsec_...",                           │
│  │     // NeoNet specific:                                       │
│  │     "profile_id": "...",                                     │
│  │     "access_key": "...",                                     │
│  │     "merchant_id": "...",                                    │
│  │     "terminal_id": "..."                                     │
│  │   }                                                          │
│  └─ settings: JSONB                                             │
└─────────────────────────────────────────────────────────────────┘
```

### 3.2 Gateways Soportados

| Gateway | Región | Monedas | Estado | Notas |
|---------|--------|---------|--------|-------|
| **Stripe** | Global | USD, EUR, + | ✅ Soportado | Connect accounts por venue |
| **NeoNet/Cybersource** | Guatemala, LATAM | GTQ, USD | ✅ Soportado | Ya integrado en sistema actual |
| **MercadoPago** | LATAM | ARS, BRL, MXN, CLP | 🔄 Planificado | Popular en Argentina, Brasil |

### 3.3 PaymentGatewayRouter Service

```go
// services/payment_router.go (YA IMPLEMENTADO)

type PaymentGateway interface {
    GetName() string
    CreateCheckout(ctx context.Context, order *CheckoutRequest) (*CheckoutResponse, error)
    ConfirmPayment(ctx context.Context, sessionID string) (*PaymentResult, error)
    CreateRefund(ctx context.Context, transactionID string, amount float64) (*RefundResult, error)
    ValidateWebhook(req *http.Request, secret string) ([]byte, error)
}

type PaymentRouter struct {
    central      *SupabaseClient
    gateways     map[string]map[string]PaymentGateway  // venue_id -> gateway_name -> gateway
    configs      map[string]*cachedPaymentConfig
    // ... cache management
}

func (r *PaymentRouter) ForVenue(ctx context.Context, venueID string) (PaymentGateway, error) {
    // 1. Check cache
    // 2. Load config from central DB
    // 3. Decrypt credentials
    // 4. Create/return gateway instance
}
```

### 3.4 Flujo de Pago Completo

```
┌──────────────────────────────────────────────────────────────────┐
│                        PAYMENT FLOW                               │
└──────────────────────────────────────────────────────────────────┘

Customer Request (POST /api/v1/orders/checkout)
       │
       ▼
┌──────────────────┐
│  API Controller  │
│  (venue_id from  │
│   request/token) │
└────────┬─────────┘
         │
         ▼
┌──────────────────┐      ┌─────────────────────────┐
│ PaymentRouter    │─────▶│ Load Venue Payment      │
│ .ForVenue()      │      │ Config from Central DB  │
└────────┬─────────┘      │ (decrypt credentials)   │
         │                └─────────────────────────┘
         ▼
    ┌────────────┐
    │  Switch    │
    │  gateway   │
    └────┬───────┘
         │
    ┌────┼────────────┬───────────────┐
    ▼    ▼            ▼               ▼
┌──────┐┌──────────┐┌─────────────┐┌─────────────┐
│Stripe││NeoNet/   ││MercadoPago  ││Future       │
│      ││Cybersourc││             ││Gateways     │
└──┬───┘└────┬─────┘└──────┬──────┘└──────┬──────┘
   │         │             │              │
   └─────────┴─────────────┴──────────────┘
                    │
                    ▼
         ┌──────────────────┐
         │ Process Payment  │
         │ (gateway API)    │
         └────────┬─────────┘
                  │
                  ▼
    ┌─────────────────────────────────────────┐
    │ On Success:                              │
    │                                          │
    │ 1. Record in VENUE DB:                   │
    │    - orders (update status)              │
    │    - tickets (create)                    │
    │    - transactions (full details)         │
    │                                          │
    │ 2. Record in CENTRAL DB:                 │
    │    - platform_transactions               │
    │      (gross, platform_fee, venue_net)    │
    │                                          │
    │ 3. Send confirmation email               │
    │ 4. Generate PDF tickets                  │
    └─────────────────────────────────────────┘
```

### 3.5 Cálculo de Comisiones

```go
// Para cada transacción:
type FeeCalculation struct {
    GrossAmount        float64  // Monto total pagado por cliente
    PlatformFeePercent float64  // Ej: 5.00 (5%)
    PlatformFeeFixed   float64  // Ej: 0.50
    PlatformFeeTotal   float64  // = (Gross * Percent/100) + Fixed
    GatewayFee         float64  // Stripe: ~2.9% + $0.30
    VenueNetAmount     float64  // = Gross - PlatformFee - GatewayFee
}

// Ejemplo:
// Cliente paga: $100.00
// Platform fee (5% + $0.50): $5.50
// Stripe fee (2.9% + $0.30): $3.20
// Venue recibe: $91.30
```

### 3.6 Webhooks por Gateway

```go
// Cada gateway tiene su propio endpoint de webhook
router.POST("/webhooks/stripe/:venue_id", HandleStripeWebhook)
router.POST("/webhooks/neonet/:venue_id", HandleNeoNetWebhook)
router.POST("/webhooks/mercadopago/:venue_id", HandleMercadoPagoWebhook)

func HandleStripeWebhook(c *gin.Context) {
    venueID := c.Param("venue_id")

    // 1. Get venue's webhook secret from payment config
    config, err := services.Payments.GetConfig(ctx, venueID)

    // 2. Validate signature with venue-specific secret
    payload, err := services.Payments.ValidateWebhook(c.Request, config.WebhookSecret)

    // 3. Process event
    // 4. Update order/transaction in venue DB
    // 5. Update platform_transactions in central DB
}
```

---

## 4. Schema de Base de Datos Actual

### 4.1 Estado Actual: Single Database

> **IMPORTANTE**: Actualmente TODO está en UNA SOLA base de datos Supabase.
> La migración a multi-tenant es parte del plan.

### 4.2 Tablas Existentes (42 tablas)

#### Entidades Principales

| Tabla | Descripción | Registros Clave |
|-------|-------------|-----------------|
| `organizations` | Empresas dueñas de venues | owner_id → public_users |
| `venues` | Locales/discotecas | organization_id, payment_gateway |
| `events` | Eventos de cada venue | venue_id, ticket_limit |
| `ticket_types` | Tipos de entrada por evento | event_id, price, available_quantity |
| `event_vip_ticket_types` | Tickets VIP con precio por género | male_price, female_price |

#### Usuarios y Staff

| Tabla | Descripción |
|-------|-------------|
| `public_users` | Usuarios que compran entradas |
| `organization_workers` | Empleados de venues |
| `roles` | Roles de empleados |
| `pull_staff` | Staff de la plataforma Pull |

#### Órdenes y Tickets

| Tabla | Descripción |
|-------|-------------|
| `orders` | Compras de entradas individuales |
| `tickets` | Entradas generadas con QR |

#### Reservas de Grupo (Group Reservations)

| Tabla | Descripción |
|-------|-------------|
| `group_reservations` | Reservas de mesa/grupo |
| `group_reservation_guests` | Invitados de la reserva |
| `group_reservation_bottles` | Botellas incluidas |
| `group_reservation_mixers` | Mixers incluidos |
| `group_reservation_status` | Estados posibles |

#### VIP List (Flow Alternativo)

| Tabla | Descripción |
|-------|-------------|
| `vip_list_reservations` | Reservas VIP list |
| `vip_list_guests` | Invitados VIP |
| `vip_list_bottles` | Botellas VIP |

#### Guest List

| Tabla | Descripción |
|-------|-------------|
| `guest_list_types` | Tipos de guest list |
| `guest_list_signups` | Inscripciones |

#### Productos

| Tabla | Descripción |
|-------|-------------|
| `vip_bottles` | Catálogo de botellas |
| `vip_mixers` | Catálogo de mixers |

#### Transacciones y Pagos

| Tabla | Descripción |
|-------|-------------|
| `transactions` | Transacciones completas |
| `transaction_line_items` | Líneas de detalle |
| `payment_transactions` | Transacciones NeoNet |
| `payment_audit_log` | Audit log de pagos |
| `payment_gateway_credentials` | Credenciales de gateway |

#### Revenue Summaries

| Tabla | Descripción |
|-------|-------------|
| `daily_revenue_summary` | Resumen diario por venue |
| `monthly_revenue_summary` | Resumen mensual por venue |
| `event_revenue_summary` | Resumen por evento |
| `platform_daily_revenue` | Resumen diario plataforma |
| `platform_monthly_revenue` | Resumen mensual plataforma |

#### Sesiones y Tokens

| Tabla | Descripción |
|-------|-------------|
| `user_sessions` | Sesiones de usuarios |
| `user_access_tokens` | Access tokens |
| `user_refresh_tokens` | Refresh tokens |
| `verification_codes` | Códigos de verificación |
| `pull_staff_sessions` | Sesiones de platform staff |

#### Otros

| Tabla | Descripción |
|-------|-------------|
| `staff_notifications` | Notificaciones para staff |
| `staff_push_tokens` | Push tokens |
| `user_venue_spending` | Gasto por usuario/venue |
| `support_requests` | Tickets de soporte |
| `pull_staff_audit_log` | Audit log de platform |

### 4.3 Campos de Payment Gateway en Tablas Existentes

```sql
-- venues
payment_gateway: payment_gateway_type  -- 'stripe' | 'neonet'
payment_gateway_config: JSONB

-- orders
payment_gateway: payment_gateway_type
neonet_transaction_id: text
neonet_authorization_code: text

-- transactions
payment_gateway: payment_gateway_type
neonet_transaction_id: text
neonet_authorization_code: text
neonet_reference: text
stripe_payment_intent: text
stripe_charge_id: text
stripe_session_id: text
stripe_transfer_id: text

-- payment_gateway_credentials
gateway: varchar  -- 'cybersource'
environment: varchar  -- 'test' | 'production'
profile_id, access_key, secret_key_encrypted, merchant_id, terminal_id
```

### 4.4 Transaction Types

```sql
-- transactions.transaction_type puede ser:
'individual_ticket'   -- Compra individual de entrada
'group_organizer'     -- Pago del organizador de grupo
'group_guest'         -- Pago de invitado de grupo
'refund'              -- Reembolso total
'partial_refund'      -- Reembolso parcial
'adjustment'          -- Ajuste manual

-- transactions.status puede ser:
'pending', 'captured', 'refunded', 'partially_refunded',
'failed', 'cancelled', 'expired'
```

---

## 5. Migración: Single DB → Multi-Tenant

### 5.1 Estrategia de Migración

```
FASE ACTUAL (Single DB):
┌─────────────────────────────────────────┐
│         SUPABASE CENTRAL                │
│  (Todo junto: venues, events,           │
│   orders, tickets, etc.)                │
└─────────────────────────────────────────┘

                    │
                    │ MIGRACIÓN
                    ▼

FASE FUTURA (Multi-Tenant):
┌─────────────────────────────────────────┐
│         SUPABASE CENTRAL                │
│  (Solo: venues registry, configs,       │
│   platform_transactions, pull_staff)    │
└─────────────────────────────────────────┘
          │
    ┌─────┼─────┐
    ▼     ▼     ▼
┌──────┐┌──────┐┌──────┐
│ DB A ││ DB B ││ DB C │  ← Una DB por venue
└──────┘└──────┘└──────┘
```

### 5.2 Plan de Migración

#### Paso 1: Preparar Central DB
```sql
-- Crear tablas en Central que NO existen
CREATE TABLE venue_database_configs (...);
CREATE TABLE venue_payment_configs (...);  -- Migrar de payment_gateway_credentials

-- Migrar datos de venues (solo campos necesarios)
-- Agregar campos: platform_fee_percent, platform_fee_fixed
```

#### Paso 2: Crear Template de Venue DB
```sql
-- Script SQL que crea todas las tablas necesarias para un venue
-- Esto se ejecutará cuando se cree un nuevo venue

-- Incluye: events, ticket_types, orders, tickets,
-- group_reservations, vip_list_*, public_users, etc.
```

#### Paso 3: Migrar Venues Existentes
```python
# Para cada venue existente:
# 1. Crear nueva base de datos Supabase
# 2. Ejecutar template SQL
# 3. Copiar datos del venue desde DB central
# 4. Registrar config en venue_database_configs
# 5. Verificar integridad
```

#### Paso 4: Actualizar API
```go
// El código actual ya está preparado para multi-tenant
// Solo necesita que los datos estén migrados
```

### 5.3 Tablas que se Quedan en Central

```sql
-- CENTRAL DATABASE (después de migración)

-- Venues y Organizations (metadata solamente)
organizations (id, name, owner_id, billing_info)
venues (id, name, slug, platform_fee_percent, platform_fee_fixed, subscription_status)

-- Database Configs
venue_database_configs (venue_id, supabase_url, service_key, anon_key)

-- Payment Configs (migrado de payment_gateway_credentials)
venue_payment_configs (venue_id, gateway, credentials, is_primary)

-- Platform Transactions (copia de transactions para revenue)
platform_transactions (venue_id, gross, platform_fee, venue_net, gateway)

-- Platform Revenue Summaries
platform_daily_revenue (date, totals)
platform_monthly_revenue (year, month, totals)

-- Platform Staff
pull_staff (email, password_hash, role)
pull_staff_sessions (staff_id, token_hash)
pull_staff_audit_log (staff_id, action)

-- Support
support_requests (user_id, subject, status)
```

### 5.4 Tablas que van a Venue DB

```sql
-- VENUE DATABASE (una por venue)

-- Events
events, ticket_types, event_vip_ticket_types

-- Orders & Tickets
orders, tickets

-- Group Reservations
group_reservations, group_reservation_guests,
group_reservation_bottles, group_reservation_mixers,
group_reservation_status

-- VIP List
vip_list_reservations, vip_list_guests, vip_list_bottles

-- Guest List
guest_list_types, guest_list_signups

-- Products
vip_bottles, vip_mixers

-- Users & Staff
public_users, organization_workers, roles

-- Transactions (detalle completo)
transactions, transaction_line_items,
payment_transactions, payment_audit_log

-- Revenue Summaries
daily_revenue_summary, monthly_revenue_summary, event_revenue_summary

-- Auth
user_sessions, user_access_tokens, user_refresh_tokens, verification_codes

-- Notifications
staff_notifications, staff_push_tokens

-- User Data
user_venue_spending
```

---

## 6. Estado Actual del Proyecto

### 6.1 Resumen de Progreso

```
[████████████████░░░░░░░░░░░░░░] 40% Completado

✅ Fase 1: Estructura del proyecto - COMPLETADA
✅ Fase 2: Infraestructura base - COMPLETADA
🔄 Fase 3: Schema Central DB - PENDIENTE ← SIGUIENTE PASO
⬜ Fase 4: Handlers de autenticación - PENDIENTE
⬜ Fase 5: Handlers de venues y eventos - PENDIENTE
⬜ Fase 6: Handlers de órdenes y pagos - PENDIENTE
⬜ Fase 7: Handlers de tickets - PENDIENTE
⬜ Fase 8: Handlers de staff/admin - PENDIENTE
⬜ Fase 9: Handlers de plataforma - PENDIENTE
⬜ Fase 10: Testing y despliegue - PENDIENTE
```

### 6.2 Lo que YA está implementado

| Componente | Archivo | Estado |
|------------|---------|--------|
| Configuración | `config/config.go` | ✅ |
| Supabase Client | `services/supabase.go` | ✅ Optimizado |
| Database Router | `services/database_router.go` | ✅ Multi-tenant ready |
| Payment Router | `services/payment_router.go` | ✅ Multi-gateway ready |
| Crypto | `services/crypto.go` | ✅ AES-256-GCM |
| JWT Service | `services/jwt.go` | ✅ |
| Auth Middleware | `middleware/auth.go` | ✅ Staff/User/Platform |
| Rate Limiter | `middleware/rate_limiter.go` | ✅ |
| Models | `models/*.go` | ✅ |
| Routes | `main.go` | ✅ |
| Handlers | `main.go` | ⚠️ **PLACEHOLDER** |
| Central DB Schema | Supabase | ⚠️ **PENDIENTE** |
| Venue DB Template | SQL | ⚠️ **PENDIENTE** |

### 6.3 Conexión Verificada

```bash
curl http://localhost:8080/health

# Response:
{
  "status": "ok",
  "databases": { "central": true },
  "stats": {
    "database": {
      "central_configured": true,
      "multi_tenant_enabled": true
    }
  }
}
```

---

## 7. Estructura de Archivos

```
Pull-API-v2/
├── main.go                      # Entry point + Routes + Placeholder handlers
├── go.mod                       # Dependencias
├── go.sum                       # Checksums
├── .env                         # Variables de entorno (NO commitear)
├── .env.example                 # Template
├── PLAN_COMPLETO.md            # Este documento
│
├── config/
│   └── config.go               # Carga de configuración multi-tenant
│
├── models/
│   ├── auth.go                 # LoginRequest, TokenClaims
│   ├── venue.go                # Venue, VenueDatabaseConfig, Organization
│   ├── payment.go              # VenuePaymentConfig, PlatformTransaction
│   └── events.go               # Event, TicketType, Order, Ticket
│
├── services/
│   ├── supabase.go             # Cliente Supabase optimizado
│   ├── database_router.go      # Enrutamiento multi-tenant
│   ├── payment_router.go       # Enrutamiento de pasarelas
│   ├── crypto.go               # Encriptación AES-256-GCM
│   └── jwt.go                  # JWT tokens
│
├── middleware/
│   ├── auth.go                 # AuthenticateStaff/User/Platform
│   └── rate_limiter.go         # Rate limiting por IP
│
├── controllers/                # ⚠️ POR CREAR
│   ├── auth_controller.go
│   ├── venue_controller.go
│   ├── event_controller.go
│   ├── order_controller.go
│   ├── ticket_controller.go
│   ├── staff_controller.go
│   └── platform_controller.go
│
└── sql/                        # ⚠️ POR CREAR
    ├── central_schema.sql      # Schema para Central DB
    └── venue_template.sql      # Template para Venue DBs
```

---

## 8. Configuración del Entorno

### 8.1 Variables de Entorno (.env)

```env
# =============================================
# SERVER
# =============================================
PORT=8080
ENVIRONMENT=development
FRONTEND_URL=https://web.pullevents.com
ALLOWED_ORIGINS=https://web.pullevents.com,https://pullevents.com,http://localhost:3000

# =============================================
# CENTRAL DATABASE (Pull Platform)
# =============================================
CENTRAL_SUPABASE_URL=https://dqqvtehpidihahzabcxg.supabase.co
CENTRAL_SERVICE_KEY=sb_secret_xoObT9vGOLLiF42yBgpTWg_aOl8FqMg
CENTRAL_ANON_KEY=sb_publishable_BMduM7iCSpdMWBcQDMVyGQ_nY6sdTjY

# =============================================
# DEFAULT VENUE DATABASE (Legacy/Fallback)
# =============================================
DEFAULT_SUPABASE_URL=
DEFAULT_SERVICE_KEY=
DEFAULT_ANON_KEY=

# =============================================
# SECURITY
# =============================================
JWT_SECRET=your-jwt-secret-minimum-32-characters-long-here  # ⚠️ CAMBIAR
APP_KEY=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef  # ⚠️ CAMBIAR

# =============================================
# STRIPE (Platform - para crear nuevos venues)
# =============================================
STRIPE_SECRET_KEY=
STRIPE_PUBLISHABLE_KEY=
STRIPE_WEBHOOK_SECRET=

# =============================================
# EMAIL (Resend)
# =============================================
RESEND_API_KEY=
RESEND_FROM_EMAIL=Pull Events <noreply@tickets.pullevents.com>

# =============================================
# OPTIONAL
# =============================================
REDIS_URL=
```

### 8.2 Credenciales Conocidas

| Servicio | Valor |
|----------|-------|
| Supabase Central URL | `https://dqqvtehpidihahzabcxg.supabase.co` |
| Supabase Central Service Key | `sb_secret_xoObT9vGOLLiF42yBgpTWg_aOl8FqMg` |
| Supabase Central Anon Key | `sb_publishable_BMduM7iCSpdMWBcQDMVyGQ_nY6sdTjY` |

---

## 9. Fases de Desarrollo Detalladas

### Fase 1: Estructura del Proyecto ✅ COMPLETADA

- [x] Crear carpeta `Pull-API-v2`
- [x] Inicializar módulo Go
- [x] Crear estructura de carpetas
- [x] Configurar dependencias

### Fase 2: Infraestructura Base ✅ COMPLETADA

- [x] `config/config.go` - Multi-tenant config
- [x] `services/supabase.go` - Cliente optimizado con:
  - [x] Connection pooling (500 max idle)
  - [x] Buffer pooling (sync.Pool)
  - [x] HTTP/2 support
  - [x] Regex validation (SQL injection prevention)
  - [x] Context support for cancellation
- [x] `services/database_router.go` - Multi-tenant con:
  - [x] Shared HTTP transport
  - [x] Double-checked locking
  - [x] Parallel health checks
  - [x] Cache cleanup goroutine
- [x] `services/payment_router.go` - Multi-gateway con:
  - [x] Gateway interface
  - [x] Stripe gateway stub
  - [x] NeoNet gateway stub
  - [x] MercadoPago gateway stub
  - [x] Credential encryption/decryption
- [x] `services/crypto.go` - AES-256-GCM
- [x] `services/jwt.go` - Three token types
- [x] `middleware/auth.go` - Staff/User/Platform
- [x] `middleware/rate_limiter.go`
- [x] `models/*.go`
- [x] `main.go` - Routes y placeholders
- [x] `.env` configurado
- [x] Verificar conexión a central DB

### Fase 3: Schema Central DB 🔄 SIGUIENTE

- [ ] Crear `sql/central_schema.sql` con:
  - [ ] `organizations` (owner_id, billing)
  - [ ] `venues` (platform fees, subscription)
  - [ ] `venue_database_configs` (encrypted)
  - [ ] `venue_payment_configs` (encrypted)
  - [ ] `platform_transactions`
  - [ ] `platform_daily_revenue`
  - [ ] `platform_monthly_revenue`
  - [ ] `pull_staff`
  - [ ] `pull_staff_sessions`
  - [ ] `pull_staff_audit_log`
  - [ ] `support_requests`
- [ ] Ejecutar schema en Supabase central
- [ ] Verificar tablas creadas
- [ ] Insertar datos de prueba

### Fase 4: Template Venue DB ⬜

- [ ] Crear `sql/venue_template.sql` con TODAS las tablas:
  - [ ] Events: `events`, `ticket_types`, `event_vip_ticket_types`
  - [ ] Orders: `orders`, `tickets`
  - [ ] Group Reservations: `group_reservations`, `group_reservation_guests`, etc.
  - [ ] VIP List: `vip_list_reservations`, `vip_list_guests`, etc.
  - [ ] Guest List: `guest_list_types`, `guest_list_signups`
  - [ ] Products: `vip_bottles`, `vip_mixers`
  - [ ] Users: `public_users`, `organization_workers`, `roles`
  - [ ] Transactions: `transactions`, `transaction_line_items`, `payment_transactions`
  - [ ] Revenue: `daily_revenue_summary`, `monthly_revenue_summary`, `event_revenue_summary`
  - [ ] Auth: `user_sessions`, `user_access_tokens`, `user_refresh_tokens`, `verification_codes`
  - [ ] Notifications: `staff_notifications`, `staff_push_tokens`
  - [ ] Other: `user_venue_spending`, `payment_audit_log`
- [ ] Crear venue de prueba
- [ ] Verificar template funciona

### Fase 5: Handlers de Autenticación ⬜

- [ ] Crear `controllers/auth_controller.go`
- [ ] `POST /auth/login-staff` - Login staff de venue
  - Input: `{email, password, venue_id}`
  - Output: `{token, staff, venue}`
  - Lógica: Query venue DB → verify password → generate JWT
- [ ] `GET /auth/verify` - Verificar token válido
- [ ] `POST /auth/refresh` - Refrescar token
- [ ] `POST /user-auth/request-code` - Enviar código 6 dígitos
  - Input: `{email}`
  - Lógica: Generar código → guardar en `verification_codes` → enviar email
- [ ] `POST /user-auth/verify-code` - Verificar código
  - Input: `{email, code}`
  - Output: `{token, user}`
- [ ] `GET /user-auth/profile` - Obtener perfil
- [ ] `PUT /user-auth/profile` - Actualizar perfil
- [ ] Actualizar imports en `main.go`
- [ ] Tests manuales con cURL

### Fase 6: Handlers de Venues ⬜

- [ ] Crear `controllers/venue_controller.go`
- [ ] `GET /venues` - Listar venues públicos
  - Query Central DB → venues activos
- [ ] `GET /venues/:slug` - Obtener venue
- [ ] `GET /venues/:slug/events` - Eventos del venue
  - Obtener venue_id → Query Venue DB → events
- [ ] `GET /venue/info` (staff) - Info del venue
- [ ] `PUT /venue/update` (staff) - Actualizar venue

### Fase 7: Handlers de Eventos ⬜

- [ ] Crear `controllers/event_controller.go`
- [ ] `GET /events` - Listar eventos públicos
- [ ] `GET /events/:slug` - Obtener evento
- [ ] `GET /events/:slug/tickets` - Tipos de ticket
- [ ] `POST /event` (staff) - Crear evento
- [ ] `PUT /event/:id` (staff) - Actualizar
- [ ] `DELETE /event/:id` (staff) - Eliminar (soft delete)

### Fase 8: Handlers de Órdenes y Pagos ⬜

- [ ] Crear `controllers/order_controller.go`
- [ ] `POST /orders/create` - Crear orden (reserva)
  - Input: `{event_id, ticket_type_id, quantity, user_info}`
  - Lógica: Verificar disponibilidad → crear orden pending
- [ ] `POST /orders/checkout` - Iniciar pago
  - Lógica: PaymentRouter.ForVenue() → gateway.CreateCheckout()
- [ ] `GET /orders/confirm` - Callback de pago
  - Lógica: Verificar pago → crear tickets → enviar email
- [ ] `GET /orders/:code` - Obtener orden
- [ ] `POST /orders/webhook` - Webhook de pagos
- [ ] `GET /orders-admin/venue` (staff) - Órdenes del venue
- [ ] `POST /orders-admin/:id/approve` (staff)
- [ ] `POST /orders-admin/:id/reject` (staff)
- [ ] Implementar cálculo de comisiones
- [ ] Registrar en `platform_transactions`

### Fase 9: Handlers de Tickets ⬜

- [ ] Crear `controllers/ticket_controller.go`
- [ ] `GET /tickets/my` (user) - Mis tickets
- [ ] `GET /tickets/:id/pdf` (user) - Generar PDF
- [ ] `POST /validate/ticket` (staff) - Validar ticket
  - Input: `{qr_token}`
  - Lógica: Buscar ticket → verificar no usado → marcar checked_in

### Fase 10: Handlers de Group Reservations ⬜

- [ ] Crear `controllers/group_controller.go`
- [ ] Endpoints para:
  - Crear reserva de grupo
  - Agregar invitados
  - Pago del organizador
  - Pago de invitados
  - Aprobar/rechazar reserva
  - Gestión de botellas

### Fase 11: Handlers de VIP List ⬜

- [ ] Crear `controllers/vip_list_controller.go`
- [ ] Endpoints similares a group pero con flow VIP

### Fase 12: Handlers de Guest List ⬜

- [ ] Crear `controllers/guest_list_controller.go`
- [ ] Endpoints para inscripciones a guest list

### Fase 13: Handlers de Staff ⬜

- [ ] Crear `controllers/staff_controller.go`
- [ ] `GET /staff/dashboard` - Dashboard del venue
  - Ventas del día, pendientes, próximos eventos
- [ ] `GET /staff/notifications` - Notificaciones
- [ ] `GET /staff/analytics` - Analytics
- [ ] `GET /employees` - Listar empleados
- [ ] `POST /employees` - Crear empleado
- [ ] `PUT /employees/:id` - Actualizar
- [ ] `DELETE /employees/:id` - Eliminar (soft delete)

### Fase 14: Handlers de Plataforma ⬜

- [ ] Crear `controllers/platform_controller.go`
- [ ] `POST /platform/login` - Login admin Pull
- [ ] `GET /admin/dashboard` - Dashboard plataforma
  - Ventas globales, venues activos, revenue
- [ ] `GET /admin/venues` - Gestión de venues
- [ ] `GET /admin/revenue` - Revenue de Pull
- [ ] `GET /admin/transactions` - Transacciones

### Fase 15: Email Service ⬜

- [ ] Crear `services/email.go`
- [ ] Integrar Resend API
- [ ] Templates para:
  - Código de verificación
  - Confirmación de compra
  - Tickets PDF adjuntos
  - Recordatorios de evento

### Fase 16: PDF Generation ⬜

- [ ] Crear `services/pdf.go`
- [ ] Generar tickets PDF con:
  - QR code
  - Info del evento
  - Info del ticket
  - Branding del venue

### Fase 17: Testing ⬜

- [ ] Unit tests para services
- [ ] Unit tests para handlers
- [ ] Integration tests
- [ ] Load testing
- [ ] Security testing

### Fase 18: Despliegue ⬜

- [ ] Dockerfile
- [ ] docker-compose.yml
- [ ] CI/CD pipeline
- [ ] Monitoring/alerting
- [ ] Backups

---

## 10. APIs y Endpoints

### 10.1 Públicos (sin auth)

| Método | Endpoint | Descripción |
|--------|----------|-------------|
| GET | `/health` | Health check |
| GET | `/api/v1/venues` | Listar venues |
| GET | `/api/v1/venues/:slug` | Obtener venue |
| GET | `/api/v1/venues/:slug/events` | Eventos de venue |
| GET | `/api/v1/events` | Listar eventos |
| GET | `/api/v1/events/:slug` | Obtener evento |
| GET | `/api/v1/events/:slug/tickets` | Tipos de ticket |
| POST | `/api/v1/auth/login-staff` | Login staff |
| POST | `/api/v1/user-auth/request-code` | Solicitar código |
| POST | `/api/v1/user-auth/verify-code` | Verificar código |
| POST | `/api/v1/orders/create` | Crear orden |
| POST | `/api/v1/orders/checkout` | Iniciar pago |
| GET | `/api/v1/orders/confirm` | Callback pago |
| GET | `/api/v1/orders/:code` | Obtener orden |
| POST | `/api/v1/orders/webhook` | Webhook pagos |
| POST | `/api/v1/platform/login` | Login platform |

### 10.2 Usuario Autenticado

| Método | Endpoint | Descripción |
|--------|----------|-------------|
| GET | `/api/v1/user-auth/profile` | Obtener perfil |
| PUT | `/api/v1/user-auth/profile` | Actualizar perfil |
| GET | `/api/v1/tickets/my` | Mis tickets |
| GET | `/api/v1/tickets/:id/pdf` | Descargar PDF |

### 10.3 Staff de Venue

| Método | Endpoint | Descripción |
|--------|----------|-------------|
| GET | `/api/v1/auth/verify` | Verificar token |
| POST | `/api/v1/auth/refresh` | Refrescar token |
| GET | `/api/v1/venue/info` | Info del venue |
| PUT | `/api/v1/venue/update` | Actualizar venue |
| POST | `/api/v1/event` | Crear evento |
| PUT | `/api/v1/event/:id` | Actualizar evento |
| DELETE | `/api/v1/event/:id` | Eliminar evento |
| GET | `/api/v1/orders-admin/venue` | Órdenes del venue |
| POST | `/api/v1/orders-admin/:id/approve` | Aprobar orden |
| POST | `/api/v1/orders-admin/:id/reject` | Rechazar orden |
| POST | `/api/v1/validate/ticket` | Validar ticket |
| GET | `/api/v1/staff/dashboard` | Dashboard |
| GET | `/api/v1/staff/notifications` | Notificaciones |
| GET | `/api/v1/staff/analytics` | Analytics |
| GET | `/api/v1/employees` | Listar empleados |
| POST | `/api/v1/employees` | Crear empleado |
| PUT | `/api/v1/employees/:id` | Actualizar empleado |
| DELETE | `/api/v1/employees/:id` | Eliminar empleado |

### 10.4 Platform Admin

| Método | Endpoint | Descripción |
|--------|----------|-------------|
| GET | `/api/v1/admin/dashboard` | Dashboard Pull |
| GET | `/api/v1/admin/venues` | Gestión venues |
| GET | `/api/v1/admin/revenue` | Revenue Pull |
| GET | `/api/v1/admin/transactions` | Transacciones |

### 10.5 Webhooks

| Método | Endpoint | Descripción |
|--------|----------|-------------|
| POST | `/webhooks/stripe/:venue_id` | Stripe webhook |
| POST | `/webhooks/neonet/:venue_id` | NeoNet webhook |
| POST | `/webhooks/mercadopago/:venue_id` | MercadoPago webhook |

---

## 11. Guía de Implementación

### 11.1 Patrón de Handler

```go
func ExampleHandler(c *gin.Context) {
    // 1. Context con timeout
    ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
    defer cancel()

    // 2. Parsear input
    var input InputStruct
    if err := c.ShouldBindJSON(&input); err != nil {
        c.JSON(400, gin.H{"error": "Invalid input"})
        return
    }

    // 3. Obtener venue_id (del token o request)
    venueID := c.GetString("venue_id")
    if venueID == "" {
        venueID = c.Query("venue_id")
    }

    // 4. Obtener cliente de BD correcto
    db := services.DB.ForVenue(venueID)

    // 5. Ejecutar operación
    result, err := db.QueryCtx(ctx, "table", params)
    if err != nil {
        c.JSON(500, gin.H{"error": "Database error"})
        return
    }

    // 6. Respuesta
    c.JSON(200, gin.H{"data": result})
}
```

### 11.2 Acceso a Datos del Token

```go
// Staff token
staffID := c.GetString("staff_id")
venueID := c.GetString("venue_id")
role := c.GetString("role")

// User token
userID := c.GetString("user_id")
email := c.GetString("email")

// Platform token
platformStaffID := c.GetString("platform_staff_id")
platformRole := c.GetString("platform_role")
```

### 11.3 Uso de DatabaseRouter

```go
// Central database (platform data)
central := services.DB.Central()

// Venue database
venueDB := services.DB.ForVenue(venueID)

// Default database (legacy/fallback)
defaultDB := services.DB.Default()
```

### 11.4 Uso de PaymentRouter

```go
// Get payment gateway for venue
gateway, err := services.Payments.ForVenue(ctx, venueID)
if err != nil {
    // Handle error or use fallback
}

// Create checkout session
checkout, err := gateway.CreateCheckout(ctx, &CheckoutRequest{
    Amount:   totalAmount,
    Currency: "GTQ",
    OrderID:  orderID,
    // ...
})

// On webhook/callback
result, err := gateway.ConfirmPayment(ctx, sessionID)
```

### 11.5 Registrar Platform Transaction

```go
// Después de cada pago exitoso
func recordPlatformTransaction(ctx context.Context, order *Order, paymentResult *PaymentResult) error {
    // Get venue fees
    feePercent, feeFixed, _ := services.DB.GetVenueFees(ctx, order.VenueID)

    // Calculate
    platformFee := (order.Total * feePercent / 100) + feeFixed
    venueNet := order.Total - platformFee - paymentResult.GatewayFee

    // Record in central
    return services.DB.RecordPlatformTransaction(ctx, &models.PlatformTransaction{
        VenueID:             order.VenueID,
        TransactionType:     "ticket_sale",
        OriginalAmount:      order.Total,
        PlatformFeePercent:  feePercent,
        PlatformFeeFixed:    feeFixed,
        PlatformFeeTotal:    platformFee,
        VenueNetAmount:      venueNet,
        Currency:            order.Currency,
        PaymentGateway:      paymentResult.Gateway,
        ExternalTransactionID: paymentResult.TransactionID,
        VenueOrderID:        order.ID,
        Status:              "completed",
    })
}
```

---

## 12. Seguridad

### 12.1 Medidas Implementadas

| Medida | Ubicación |
|--------|-----------|
| CORS | `main.go` - Solo orígenes permitidos |
| Security Headers | `main.go` - X-Frame-Options, HSTS |
| Rate Limiting | `middleware/rate_limiter.go` |
| JWT Validation | `middleware/auth.go` |
| SQL Injection Prevention | `services/supabase.go` - Regex validation |
| Credential Encryption | `services/crypto.go` - AES-256-GCM |
| TLS 1.2+ | Todas las conexiones HTTP |

### 12.2 Checklist de Seguridad

- [ ] Cambiar `JWT_SECRET` en producción (mínimo 32 chars)
- [ ] Generar nuevo `APP_KEY` con `openssl rand -hex 32`
- [ ] Configurar HTTPS
- [ ] Habilitar RLS en Supabase
- [ ] Auditar logs de acceso
- [ ] Configurar backup de bases de datos

---

## 13. Testing y Despliegue

### 13.1 Comandos de Test

```bash
# Build
cd Pull-API-v2
go build -v ./...

# Run
go run .

# Test health
curl http://localhost:8080/health

# Test con cURL
curl -X POST http://localhost:8080/api/v1/auth/login-staff \
  -H "Content-Type: application/json" \
  -d '{"email":"admin@venue.com","password":"secret"}'
```

### 13.2 Dockerfile

```dockerfile
FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o pull-api-v2

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/pull-api-v2 .
EXPOSE 8080
CMD ["./pull-api-v2"]
```

---

## Apéndice: Próximo Paso Concreto

### Lo que debes hacer AHORA:

1. **Crear el schema en Supabase Central**
   - Ir a: https://supabase.com/dashboard/project/dqqvtehpidihahzabcxg
   - SQL Editor
   - Ejecutar el SQL de la Fase 3

2. **Crear `controllers/auth_controller.go`**
   - Implementar `loginStaffHandler`
   - Actualizar imports en `main.go`

3. **Probar el login**
   ```bash
   curl -X POST http://localhost:8080/api/v1/auth/login-staff \
     -H "Content-Type: application/json" \
     -d '{"email":"test@venue.com","password":"test123"}'
   ```

---

## Historial de Cambios

| Fecha | Cambio |
|-------|--------|
| 2026-02-04 | Creación del documento |
| 2026-02-04 | Completada Fase 1 y 2 |
| 2026-02-04 | Añadida sección Payment Gateways Per-Venue |
| 2026-02-04 | Añadido schema completo de BD actual |
| 2026-02-04 | Añadido plan de migración Single→Multi-tenant |
| 2026-02-04 | Detalladas todas las fases de desarrollo |
