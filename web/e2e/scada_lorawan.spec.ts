import { execFileSync } from 'node:child_process'
import { expect, test, type Page } from '@playwright/test'
import { ADMIN, deleteDashboardsNamed, login, useTheme } from './helpers'

const MOSQUITTO = process.env.MOSQUITTO_CONTAINER ?? 'quack-mosquitto-1'
const DIR = 'e2e/screenshots'

function mqttPub(topic: string, ...payload: string[]) {
  execFileSync('docker', [
    'exec',
    MOSQUITTO,
    'mosquitto_pub',
    '-V',
    'mqttv5',
    '-q',
    '1',
    '-t',
    topic,
    ...payload,
  ])
}

async function snap(page: Page, name: string, wait = 1000) {
  await page.waitForTimeout(wait)
  await page.screenshot({ path: `${DIR}/${name}.png` })
}

const SCADA_DASH = 'SCADA Water Pumping Station'
const LORA_DASH = 'LoRaWAN Smart Agriculture'

let scadaDashId = ''
let loraDashId = ''
let decoderId = ''

test.describe.configure({ mode: 'serial' })

test.beforeAll(async ({ baseURL, playwright }) => {
  await deleteDashboardsNamed(baseURL!, [SCADA_DASH, LORA_DASH])
  const api = await playwright.request.newContext({ baseURL })
  try {
    await api.post('/api/v1/auth/login', { data: ADMIN })

    // Clean up duplicate devices
    const res = await api.get('/api/v1/admin/devices')
    if (res.ok()) {
      const list = (await res.json()) as { id: string; name: string }[]
      for (const d of list) {
        if (d.name === 'Water Treatment SCADA' || d.name === 'LoRaWAN Soil Node') {
          await api.delete(`/api/v1/admin/devices/${d.id}`)
        }
      }
    }

    // Clean up duplicate rules and downlinks on Demo broker
    const connsRes = await api.get('/api/v1/admin/mqtt/connections')
    if (connsRes.ok()) {
      const conns = (await connsRes.json()) as { id: string; name: string }[]
      const connId = conns.find((c) => c.name === 'Demo broker')?.id
      if (connId) {
        const cRes = await api.get(`/api/v1/admin/mqtt/connections/${connId}`)
        if (cRes.ok()) {
          const c = await cRes.json()
          for (const u of c.uplinks ?? []) {
            if (u.topic_filter.includes('scada') || u.topic_filter.includes('agriculture')) {
              await api.delete(`/api/v1/admin/mqtt/uplinks/${u.id}`)
            }
          }
          for (const d of c.downlinks ?? []) {
            if (d.device_external_id === 'scada-pump-station' || d.device_external_id === 'eui-70b3d57ed0054321') {
              await api.delete(`/api/v1/admin/mqtt/downlinks/${d.id}`)
            }
          }
        }
      }
    }

    // Clean up duplicate decoders
    const decRes = await api.get('/api/v1/admin/mqtt/decoders')
    if (decRes.ok()) {
      const decs = (await decRes.json()) as { id: string; name: string }[]
      for (const dec of decs) {
        if (dec.name === 'LoRaWAN Smart Agriculture Decoder') {
          await api.delete(`/api/v1/admin/mqtt/decoders/${dec.id}`)
        }
      }
    }
  } finally {
    await api.dispose()
  }
})

test('seed SCADA and LoRaWAN devices, rules, and decoders', async ({ playwright, baseURL }) => {
  const api = await playwright.request.newContext({ baseURL })
  try {
    const loginRes = await api.post('/api/v1/auth/login', { data: ADMIN })
    expect(loginRes.ok()).toBeTruthy()

    // 1. Get or create Demo broker connection
    const connsRes = await api.get('/api/v1/admin/mqtt/connections')
    const conns = (await connsRes.json()) as { id: string; name: string }[]
    let connId = conns.find((c) => c.name === 'Demo broker')?.id
    if (!connId) {
      const newConn = await api.post('/api/v1/admin/mqtt/connections', {
        data: {
          name: 'Demo broker',
          broker_url: 'mqtt://127.0.0.1:1883',
          client_id_prefix: 'quack-demo',
          replicas: 1,
          auth: { method: 'none' },
          enabled: true,
        },
      })
      connId = ((await newConn.json()) as { id: string }).id
    }
    expect(connId).toBeTruthy()

    // 2. Create LoRaWAN Decoder
    const decoderSource = `// The Things Network / ChirpStack Standard Codec
function decodeUplink(input) {
  var bytes = input.bytes;
  if (!bytes || bytes.length < 5) {
    return { errors: ["Frame too short for agriculture sensor"] };
  }
  var moisture = bytes[0];
  var temp = (bytes[1] - 100) / 10.0;
  var ec = (bytes[2] << 8) | bytes[3];
  var batt = 3.0 + ((bytes[4] >> 1) * 0.1);
  var valve = (bytes[4] & 1) === 1;

  return {
    data: {
      moisture: moisture,
      temperature: temp,
      ec: ec,
      battery: Number(batt.toFixed(2)),
      valve: valve
    },
    warnings: [],
    errors: []
  };
}

function encodeDownlink(input) {
  var open = Boolean(input.data.value);
  return { bytes: [0x01, open ? 0xFF : 0x00] };
}
`
    const decRes = await api.post('/api/v1/admin/mqtt/decoders', {
      data: {
        name: 'LoRaWAN Smart Agriculture Decoder',
        source: decoderSource,
      },
    })
    expect(decRes.ok()).toBeTruthy()
    decoderId = ((await decRes.json()) as { id: string }).id

    // 3. Create SCADA Device & Elements
    const scadaDevRes = await api.post('/api/v1/admin/devices', {
      data: {
        name: 'Water Treatment SCADA',
        description: 'Pumping Station 4 Modbus/TCP Gateway',
      },
    })
    const scadaDev = (await scadaDevRes.json()) as { id: string }

    const scadaElementsDef = [
      { name: 'Discharge Flow Rate', details: { unit: 'm³/h', minValue: 0, maxValue: 500 } },
      { name: 'Reservoir Level', details: { unit: '%', minValue: 0, maxValue: 100 } },
      { name: 'Suction Pressure', details: { unit: 'Bar', minValue: 0, maxValue: 10 } },
      { name: 'Pump VFD Frequency', details: { unit: 'Hz', minValue: 0, maxValue: 60 } },
      { name: 'Main Isolation Valve', details: { title: 'Main Discharge Isolation Valve' } },
    ]
    const scadaElemMap: Record<string, string> = {}
    for (const el of scadaElementsDef) {
      const r = await api.post('/api/v1/admin/elements', {
        data: {
          device_id: scadaDev.id,
          name: el.name,
          points: 100,
          details: el.details,
        },
      })
      scadaElemMap[el.name] = ((await r.json()) as { id: string }).id
    }

    // 4. Create LoRaWAN Device & Elements
    const loraDevRes = await api.post('/api/v1/admin/devices', {
      data: {
        name: 'LoRaWAN Soil Node',
        description: 'Smart Agriculture Multi-Depth Sensor',
      },
    })
    const loraDev = (await loraDevRes.json()) as { id: string }

    const loraElementsDef = [
      { name: 'Soil Moisture', details: { unit: '%', minValue: 0, maxValue: 100 } },
      { name: 'Soil Temperature', details: { unit: '°C', minValue: -10, maxValue: 50 } },
      { name: 'Electrical Conductivity', details: { unit: 'µS/cm', minValue: 0, maxValue: 2000 } },
      { name: 'Sensor Battery', details: { unit: 'V', minValue: 2.5, maxValue: 4.2 } },
      { name: 'Irrigation Solenoid', details: { title: 'Drip Line Solenoid Valve' } },
    ]
    const loraElemMap: Record<string, string> = {}
    for (const el of loraElementsDef) {
      const r = await api.post('/api/v1/admin/elements', {
        data: {
          device_id: loraDev.id,
          name: el.name,
          points: 100,
          details: el.details,
        },
      })
      loraElemMap[el.name] = ((await r.json()) as { id: string }).id
    }

    // 4b. Grant permissions to admin user so elements are accessible
    const loginUser = (await loginRes.json()) as { id: number }
    const userId = loginUser.id
    for (const id of Object.values(scadaElemMap)) {
      await api.put('/api/v1/admin/permissions', {
        data: { element_id: id, user_id: userId, permission: 'RC' },
      })
    }
    for (const id of Object.values(loraElemMap)) {
      await api.put('/api/v1/admin/permissions', {
        data: { element_id: id, user_id: userId, permission: 'RC' },
      })
    }

    // 5. Grant Devices to Connection
    await api.put(`/api/v1/admin/mqtt/connections/${connId}/devices`, {
      data: {
        device_id: scadaDev.id,
        external_id: 'scada-pump-station',
      },
    })
    await api.put(`/api/v1/admin/mqtt/connections/${connId}/devices`, {
      data: {
        device_id: loraDev.id,
        external_id: 'eui-70b3d57ed0054321',
      },
    })

    // 6. Create Uplink Rules
    // Rule A: SCADA JSON
    await api.post(`/api/v1/admin/mqtt/connections/${connId}/uplinks`, {
      data: {
        topic_filter: 'scada/plc/+/telemetry',
        qos: 1,
        format: 'json',
        device: { segment: 2 },
        field_map: [
          { element: 'Discharge Flow Rate', value: 'flow_m3h', wrap: 'value', when: 'exists' },
          { element: 'Reservoir Level', value: 'tank_pct', wrap: 'value', when: 'exists' },
          { element: 'Suction Pressure', value: 'pressure_bar', wrap: 'value', when: 'exists' },
          { element: 'Pump VFD Frequency', value: 'vfd_hz', wrap: 'value', when: 'exists' },
          { element: 'Main Isolation Valve', value: 'valve_open', wrap: 'value', when: 'exists' },
        ],
        enabled: true,
      },
    })

    // Rule B: LoRaWAN Binary
    await api.post(`/api/v1/admin/mqtt/connections/${connId}/uplinks`, {
      data: {
        topic_filter: 'v3/agriculture-app/devices/+/up',
        qos: 1,
        format: 'bytes',
        decoder_id: decoderId,
        device: { segment: 3 },
        field_map: [
          { element: 'Soil Moisture', value: 'moisture', wrap: 'value', when: 'exists' },
          { element: 'Soil Temperature', value: 'temperature', wrap: 'value', when: 'exists' },
          { element: 'Electrical Conductivity', value: 'ec', wrap: 'value', when: 'exists' },
          { element: 'Sensor Battery', value: 'battery', wrap: 'value', when: 'exists' },
          { element: 'Irrigation Solenoid', value: 'valve', wrap: 'value', when: 'exists' },
        ],
        enabled: true,
      },
    })

    // 7. Create Downlinks
    await api.post(`/api/v1/admin/mqtt/connections/${connId}/downlinks`, {
      data: {
        device_external_id: 'scada-pump-station',
        element: 'Main Isolation Valve',
        topic_template: 'scada/plc/{device}/cmd/{element}',
        encoder: { template: { command: 'VALVE_CONTROL', state: '{{value}}' } },
        qos: 1,
        retain: false,
      },
    })

    await api.post(`/api/v1/admin/mqtt/connections/${connId}/downlinks`, {
      data: {
        device_external_id: 'eui-70b3d57ed0054321',
        element: 'Irrigation Solenoid',
        topic_template: 'v3/agriculture-app/devices/{device}/down/push',
        encoder: { decoder_id: decoderId },
        qos: 1,
        retain: false,
      },
    })

    // 8. Create SCADA Dashboard
    const scadaDashRes = await api.post('/api/v1/dashboards', {
      data: {
        name: SCADA_DASH,
        shared: true,
        layout: {
          version: 1,
          widgets: [
            {
              id: 'w_scada_flow',
              type: 'gauge',
              element_id: scadaElemMap['Discharge Flow Rate'],
              title: 'Discharge Flow Rate (SCADA Tag: FIC-101)',
              options: { unit: 'm³/h', min: 0, max: 500, baseColor: 'blue', thresholds: [{ value: 400, color: 'red' }] },
              layouts: { lg: { x: 0, y: 0, w: 4, h: 6 } },
            },
            {
              id: 'w_scada_tank',
              type: 'gauge',
              element_id: scadaElemMap['Reservoir Level'],
              title: 'Reservoir Level (SCADA Tag: LIC-201)',
              options: { unit: '%', min: 0, max: 100, baseColor: 'cyan', thresholds: [{ value: 20, color: 'orange' }, { value: 90, color: 'red' }] },
              layouts: { lg: { x: 4, y: 0, w: 4, h: 6 } },
            },
            {
              id: 'w_scada_valve',
              type: 'switch',
              element_id: scadaElemMap['Main Isolation Valve'],
              title: 'Main Isolation Valve · SCADA Downlink',
              options: {},
              layouts: { lg: { x: 8, y: 0, w: 4, h: 6 } },
            },
            {
              id: 'w_scada_chart',
              type: 'line',
              element_id: scadaElemMap['Discharge Flow Rate'],
              title: 'Flow Rate Trend (scada/plc/+/telemetry)',
              options: { unit: 'm³/h', range: '15m', color: 'blue', area: true },
              layouts: { lg: { x: 0, y: 6, w: 6, h: 7 } },
            },
            {
              id: 'w_scada_press_chart',
              type: 'line',
              element_id: scadaElemMap['Suction Pressure'],
              title: 'Suction Pressure Trend (Bar)',
              options: { unit: 'Bar', range: '15m', color: 'purple' },
              layouts: { lg: { x: 6, y: 6, w: 6, h: 7 } },
            },
          ],
        },
      },
    })
    scadaDashId = ((await scadaDashRes.json()) as { id: string }).id

    // 9. Create LoRaWAN Dashboard
    const loraDashRes = await api.post('/api/v1/dashboards', {
      data: {
        name: LORA_DASH,
        shared: true,
        layout: {
          version: 1,
          widgets: [
            {
              id: 'w_lora_moist',
              type: 'gauge',
              element_id: loraElemMap['Soil Moisture'],
              title: 'Volumetric Soil Moisture',
              options: { unit: '%', min: 0, max: 100, baseColor: 'green', thresholds: [{ value: 30, color: 'orange' }] },
              layouts: { lg: { x: 0, y: 0, w: 3, h: 6 } },
            },
            {
              id: 'w_lora_temp',
              type: 'stat',
              element_id: loraElemMap['Soil Temperature'],
              title: 'Soil Temperature',
              options: { unit: '°C', decimals: 1, color: 'amber', sparkline: true },
              layouts: { lg: { x: 3, y: 0, w: 3, h: 6 } },
            },
            {
              id: 'w_lora_ec',
              type: 'stat',
              element_id: loraElemMap['Electrical Conductivity'],
              title: 'Salinity (EC)',
              options: { unit: 'µS/cm', decimals: 0, color: 'blue' },
              layouts: { lg: { x: 6, y: 0, w: 3, h: 6 } },
            },
            {
              id: 'w_lora_solenoid',
              type: 'switch',
              element_id: loraElemMap['Irrigation Solenoid'],
              title: 'Irrigation Valve · LoRaWAN Downlink',
              options: {},
              layouts: { lg: { x: 9, y: 0, w: 3, h: 6 } },
            },
            {
              id: 'w_lora_chart',
              type: 'line',
              element_id: loraElemMap['Soil Moisture'],
              title: 'Soil Moisture History · Binary Payload JS Decoded',
              options: { unit: '%', range: '15m', color: 'green', area: true },
              layouts: { lg: { x: 0, y: 6, w: 12, h: 7 } },
            },
          ],
        },
      },
    })
    loraDashId = ((await loraDashRes.json()) as { id: string }).id
  } finally {
    await api.dispose()
  }
})

test('publish real SCADA and LoRaWAN packets and snapshot dashboards', async ({ page }) => {
  await useTheme(page, 'light')
  await login(page, ADMIN)

  // Give gateway 2s to process mqtt-config.v1 snapshot and subscribe on broker
  await page.waitForTimeout(2000)

  // Publish multiple real SCADA packets to build history
  for (let i = 0; i < 6; i++) {
    const flow = 342.5 + i * 4.8
    const tank = 81.0 + i * 1.2
    const press = 5.2 + i * 0.15
    mqttPub(
      'scada/plc/scada-pump-station/telemetry',
      '-m',
      JSON.stringify({
        flow_m3h: flow,
        tank_pct: tank,
        pressure_bar: press,
        vfd_hz: 49.5,
        valve_open: true,
      })
    )
  }

  // Publish real binary LoRaWAN frames (5 bytes)
  // 0x2c = 44% moisture, 0xc4 = 196 (19.6°C), 0x02 0x80 = 640 µS/cm EC, 0x23 = 3.6V + valve ON
  for (let i = 0; i < 5; i++) {
    const moist = 42 + i
    const tempHex = (190 + i).toString(16)
    mqttPub(
      'v3/agriculture-app/devices/eui-70b3d57ed0054321/up',
      '-m',
      `hex:${moist.toString(16)}${tempHex}02802301`
    )
  }

  // 1. Snapshot SCADA Dashboard
  await page.goto(`/dashboards/${scadaDashId}`)
  await expect(page.getByRole('heading', { name: /Discharge Flow Rate/ })).toBeVisible({ timeout: 15_000 })
  await page.waitForTimeout(2000)
  await snap(page, 'scada-dashboard-light', 1000)

  // 2. Snapshot LoRaWAN Dashboard
  await page.goto(`/dashboards/${loraDashId}`)
  await expect(page.getByRole('heading', { name: /Volumetric Soil Moisture/ })).toBeVisible({ timeout: 15_000 })
  await page.waitForTimeout(2000)
  await snap(page, 'lorawan-dashboard-light', 1000)

  // 3. Snapshot LoRaWAN Decoder Editor
  await page.goto('/admin/mqtt/decoders')
  await expect(page.getByRole('table', { name: 'Decoders' })).toBeVisible()
  const editBtn = page.getByRole('button', { name: /Edit LoRaWAN/i }).first()
  await expect(editBtn).toBeVisible()
  await editBtn.click()
  await expect(page.getByRole('dialog')).toBeVisible()
  await expect(page.locator('#dec-src')).toBeVisible()
  await snap(page, 'lorawan-decoder-editor', 1000)

  // Close decoder dialog
  await page.keyboard.press('Escape')

  // 4. Snapshot Test & Capture Console for SCADA Rule
  const connsRes = await page.request.get('/api/v1/admin/mqtt/connections')
  const conns = (await connsRes.json()) as { id: string; name: string }[]
  const connId = conns.find((c) => c.name === 'Demo broker')?.id!
  await page.goto(`/admin/mqtt/${connId}`)
  await expect(page.getByRole('table', { name: 'Uplink rules' })).toBeVisible()

  const scadaRuleRow = page.getByRole('row').filter({ hasText: 'scada/plc/+/telemetry' }).first()
  await expect(scadaRuleRow).toBeVisible()
  const testBtn = scadaRuleRow.getByRole('button', { name: 'Test & capture' })
  await testBtn.scrollIntoViewIfNeeded()
  await testBtn.click()
  const sheet = page.getByRole('dialog')
  await expect(sheet).toBeVisible()
  await sheet.locator('#t-topic').fill('scada/plc/scada-pump-station/telemetry')
  await sheet.locator('#t-payload').fill(
    JSON.stringify({
      flow_m3h: 362.5,
      tank_pct: 88.0,
      pressure_bar: 5.8,
      vfd_hz: 50.0,
      valve_open: true,
    })
  )
  await sheet.getByRole('button', { name: 'Run the pipeline' }).click()
  await snap(page, 'scada-test-capture', 1000)

  // Close sheet
  await page.keyboard.press('Escape')

  // 5. Snapshot Downlinks Table
  const downlinksTable = page.getByRole('table', { name: 'Downlinks' })
  await downlinksTable.scrollIntoViewIfNeeded()
  await expect(downlinksTable).toBeVisible()
  await snap(page, 'mqtt-downlinks-table', 1000)

  // 6. Snapshot New Downlink Dialog
  await page.getByRole('button', { name: 'New downlink' }).click()
  await expect(page.getByRole('dialog')).toBeVisible()
  await page.locator('#d-ext').fill('scada-pump-station')
  await page.locator('#d-el').fill('VFD Frequency Setpoint')
  await page.locator('#d-topic').fill('scada/plc/{device}/cmd/{element}')
  await page.locator('#d-tpl').fill(JSON.stringify({ command: 'SET_SPEED', hz: '{{value}}' }, null, 2))
  await snap(page, 'mqtt-downlink-dialog', 1000)
  await page.keyboard.press('Escape')

  // 7. Live Downlink Execution on Dashboard (Toggle Valve Actuator)
  await page.goto(`/dashboards/${scadaDashId}`)
  const valveSwitch = page.getByRole('switch').first()
  await expect(valveSwitch).toBeVisible()
  await valveSwitch.click()
  await snap(page, 'scada-downlink-actuated', 1000)
})
