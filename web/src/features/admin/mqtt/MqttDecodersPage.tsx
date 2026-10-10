import { Code2, Pencil, Plus, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'
import { useConfirm } from '@/components/ConfirmDialog'
import { DataTable, Mono, type Column } from '@/components/DataTable'
import { Page } from '@/components/layout/AppShell'
import { PageHeader } from '@/components/states'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Field } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { toastError } from '@/lib/forms'
import { FormDialog, RowActions } from '../FormDialog'
import { useAdminMutation } from '../api'
import { formError, mqttApi, mqttKeys, useDecoders, type MqttDecoder } from './api'
import { MqttNav } from './MqttNav'

const TEMPLATE = `// TTN / ChirpStack contract: codecs from the TTN Device Repository work unchanged.
function decodeUplink(input) {
  // input.bytes: [..], input.payload (parsed JSON), input.topic, input.segments
  return {
    data: { temperature: ((input.bytes[0] << 8) | input.bytes[1]) / 100 },
    warnings: [],
    errors: []
  };
}

// Optional: dashboard commands → bytes or a payload
function encodeDownlink(input) {
  return { bytes: [input.data.value ? 1 : 0] };
}
`

function DecoderDialog({
  decoder,
  open,
  onOpenChange,
}: {
  decoder: MqttDecoder | null
  open: boolean
  onOpenChange: (o: boolean) => void
}) {
  const [name, setName] = useState(decoder?.name ?? '')
  const [source, setSource] = useState(decoder?.source ?? TEMPLATE)
  const [error, setError] = useState<string>()
  const create = useAdminMutation(mqttApi.createDecoder, [mqttKeys.all])
  const update = useAdminMutation(mqttApi.updateDecoder, [mqttKeys.all])
  return (
    <FormDialog
      open={open}
      onOpenChange={onOpenChange}
      title={decoder ? `Edit ${decoder.name}` : 'New decoder'}
      description="JavaScript (ES5.1+) run in a sandbox: no modules, network or files, 20 ms per message. Saving a new version updates every connection using it."
      submitting={create.isPending || update.isPending}
      formError={error}
      submitLabel={decoder ? `Save version ${(decoder.version ?? 0) + 1}` : 'Create decoder'}
      className="sm:max-w-3xl"
      onSubmit={async (e) => {
        e.preventDefault()
        try {
          if (decoder) await update.mutateAsync({ id: decoder.id, name, source })
          else await create.mutateAsync({ name, source })
          toast.success(decoder ? 'Decoder saved' : 'Decoder created')
          onOpenChange(false)
        } catch (err) {
          setError(formError(err))
        }
      }}
    >
      <Field label="Name" htmlFor="dec-name" required>
        <Input id="dec-name" value={name} onChange={(e) => setName(e.target.value)} />
      </Field>
      <Field
        label="Source"
        htmlFor="dec-src"
        description="decodeUplink(input) and/or encodeDownlink(input); legacy Decoder(bytes, port) also works"
      >
        <Textarea
          id="dec-src"
          rows={16}
          spellCheck={false}
          className="font-mono text-xs"
          value={source}
          onChange={(e) => setSource(e.target.value)}
        />
      </Field>
    </FormDialog>
  )
}

export function MqttDecodersPage() {
  const decoders = useDecoders()
  const del = useAdminMutation(mqttApi.deleteDecoder, [mqttKeys.all])
  const confirm = useConfirm()
  const [editing, setEditing] = useState<MqttDecoder | null>(null)
  const [open, setOpen] = useState(false)
  const columns: Column<MqttDecoder>[] = [
    {
      id: 'name',
      header: 'Name',
      sortValue: (d) => d.name ?? '',
      searchValue: (d) => d.name ?? '',
      cell: (d) => <span className="font-medium">{d.name}</span>,
    },
    { id: 'version', header: 'Version', cell: (d) => <Badge variant="muted">v{d.version}</Badge> },
    {
      id: 'functions',
      header: 'Functions',
      cell: (d) => (
        <div className="flex gap-1">
          {['decodeUplink', 'encodeDownlink', 'Decoder']
            .filter((f) => d.source.includes(`function ${f}`))
            .map((f) => (
              <Badge key={f} variant="outline" className="font-mono">
                {f}
              </Badge>
            ))}
        </div>
      ),
    },
    { id: 'size', header: 'Size', cell: (d) => <Mono>{(d.source.length / 1024).toFixed(1)} KB</Mono> },
    {
      id: 'actions',
      header: <span className="sr-only">Actions</span>,
      headClassName: 'w-20',
      cell: (d) => (
        <RowActions>
          <Button
            variant="ghost"
            size="icon-xs"
            aria-label={`Edit ${d.name}`}
            onClick={() => {
              setEditing(d)
              setOpen(true)
            }}
          >
            <Pencil />
          </Button>
          <Button
            variant="ghost"
            size="icon-xs"
            aria-label={`Delete ${d.name}`}
            onClick={() =>
              confirm.ask({
                title: `Delete decoder ${d.name}?`,
                description: 'Only possible when no rule uses it.',
                destructive: true,
                onConfirm: async () => {
                  try {
                    await del.mutateAsync(d.id)
                  } catch (err) {
                    toastError('Could not delete the decoder', err)
                    throw err
                  }
                },
              })
            }
          >
            <Trash2 />
          </Button>
        </RowActions>
      ),
    },
  ]
  return (
    <Page wide>
      <PageHeader
        title="MQTT"
        description="Decoders turn raw payloads into data, and dashboard commands into payloads."
        actions={
          <Button
            onClick={() => {
              setEditing(null)
              setOpen(true)
            }}
          >
            <Plus /> New decoder
          </Button>
        }
      />
      <MqttNav />
      <DataTable
        label="Decoders"
        data={decoders.data}
        columns={columns}
        getRowId={(d) => d.id}
        isLoading={decoders.isLoading}
        error={decoders.error}
        onRetry={() => void decoders.refetch()}
        empty={{ icon: Code2, title: 'No decoders', description: 'JSON payloads need none; binary ones do.' }}
      />
      <DecoderDialog key={editing?.id ?? 'new'} decoder={editing} open={open} onOpenChange={setOpen} />
      {confirm.dialog}
    </Page>
  )
}
