# AccessD — Visual Identity

## Brand

**Full name:** AccessD  
**Tagline:** Infrastructure Access Gateway  
**Developed by:** [DistrictD](https://districtd.co.in)

---

## Logo Concept

### Metaphor

A **gate** with a **connection indicator**: a geometric doorway or portal with a small activity dot or pulse on one side, symbolizing a controlled passage between the operator and the infrastructure.

### Variations

| Context     | Treatment                                      |
|-------------|------------------------------------------------|
| Favicon     | Single letter **A** on indigo background, rounded square |
| Sidebar     | **A** mark (8×8) + wordmark "AccessD" inline   |
| Login page  | **A** mark (12×12 px, rounded-xl) centered     |
| Full logo   | Mark + wordmark + tagline stack                |
| Dark mode   | White mark on transparent bg; indigo wordmark  |

### Current implementation

The UI uses a CSS-only logo: a rounded square in `indigo-600` containing the letter **A** in white, bold. This is intentionally minimal and works at all favicon sizes.

```jsx
<div className="flex h-8 w-8 items-center justify-center rounded-lg bg-indigo-600 text-white text-sm font-bold">
  A
</div>
```

For a proper SVG icon, the recommended direction is:

- A stylized **gateway arch** or **door frame** in a single stroke weight
- Optionally, a small **dot** in the center-right indicating an active connection
- Geometric, not illustrative — clean at 16×16 and 512×512

---

## Color Palette

| Token         | Hex       | Usage                                     |
|---------------|-----------|-------------------------------------------|
| **Primary**   | `#4f46e5` | Indigo-600 — buttons, active nav, logo bg |
| Primary dark  | `#4338ca` | Indigo-700 — button hover state           |
| Primary light | `#e0e7ff` | Indigo-100 — active nav bg, badge bg      |
| Primary tint  | `#eef2ff` | Indigo-50 — subtle highlights             |
| **Neutral 900** | `#111827` | Primary text                            |
| **Neutral 700** | `#374151` | Secondary text                          |
| **Neutral 500** | `#6b7280` | Muted text, labels                      |
| **Neutral 400** | `#9ca3af` | Placeholder, footer text                |
| **Neutral 200** | `#e5e7eb` | Borders                                 |
| **Neutral 50**  | `#f9fafb` | Page background, alternating rows       |
| **Success**   | `#059669` | Emerald-600 — active sessions, success  |
| **Warning**   | `#d97706` | Amber-600 — pending, warning states     |
| **Error**     | `#dc2626` | Red-600 — failed, terminated, errors    |
| **Info**      | `#2563eb` | Blue-600 — completed sessions, info     |

These map directly to Tailwind CSS color classes (`indigo-600`, `gray-900`, etc.) already used in the codebase.

---

## Typography

**System font stack** (no web font loading, fast and clean):

```css
font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont,
             "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
```

### Hierarchy

| Level        | Size     | Weight    | Usage                        |
|--------------|----------|-----------|------------------------------|
| Page title   | `2xl`    | `bold`    | PageHeader, login headline   |
| Card title   | `base`   | `semibold`| Card headers                 |
| Body         | `sm`     | `normal`  | Tables, form fields, content |
| Label        | `sm`     | `medium`  | Form labels, nav items       |
| Small/meta   | `xs`     | `normal`  | Badges, timestamps, footers  |
| Mono         | `xs` mono| `normal`  | IDs, UUIDs, paths            |

---

## Component Patterns

### Sidebar identity block

```
┌────────────────────────────────┐
│ [A] AccessD                    │
│                                │
│   Dashboard                    │
│   My Access                    │
│   My Sessions                  │
│                                │
│   ADMIN                        │
│     Users                      │
│     Assets                     │
│     ...                        │
│                                │
│ ─────────────────────────────  │
│ AccessD                        │
│ Infrastructure Access Gateway  │
│ DistrictD ↗                   │
└────────────────────────────────┘
```

### Login page identity block

```
┌─────────────────────────────┐
│         [ A ]               │
│    Sign in to AccessD       │
│  Infrastructure Access      │
│        Gateway              │
│                             │
│  ┌──────────────────────┐  │
│  │ Username              │  │
│  │ Password              │  │
│  │    [ Sign in ]        │  │
│  └──────────────────────┘  │
│                             │
│  AccessD • Infrastructure   │
│   Access Gateway            │
│   Developed by DistrictD ↗  │
└─────────────────────────────┘
```

---

## Dark Mode Guidance

The current UI does not implement dark mode, but the color system is designed for it:

| Light                    | Dark equivalent                          |
|--------------------------|------------------------------------------|
| `bg-white`               | `bg-gray-900`                            |
| `bg-gray-50`             | `bg-gray-800`                            |
| `text-gray-900`          | `text-gray-50`                           |
| `border-gray-200`        | `border-gray-700`                        |
| `bg-indigo-600` (logo)   | unchanged (indigo reads well in dark)    |

Use Tailwind's `dark:` variant prefix when implementing.

---

## Usage Rules

**Do:**
- Use "AccessD" (capital A, capital D, no space)
- Use the `#4f46e5` indigo as the primary identity color
- Keep the wordmark + tagline together in marketing contexts
- Always link "DistrictD" to `https://districtd.co.in` in UI footers

**Don't:**
- Write "Accessd", "ACCESSD", "Access D", or "access-d" in user-facing text
- Use the logo on backgrounds that make the indigo unreadable
- Truncate "Infrastructure Access Gateway" in primary contexts
