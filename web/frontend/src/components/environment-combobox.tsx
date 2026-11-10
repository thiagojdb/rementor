import { useEffect, useId, useRef, useState, type KeyboardEvent } from 'react'
import { Check, ChevronDown } from 'lucide-react'
import { Input } from '@/components/ui/input'

export type EnvironmentOption = { id: string; name: string; parent?: string; hosts: number; included: boolean }

export function EnvironmentCombobox({ options, value, onChange, disabled, loading }: { options: EnvironmentOption[]; value: string; onChange: (id: string) => void; disabled?: boolean; loading?: boolean }) {
  const id = useId()
  const input = useRef<HTMLInputElement>(null)
  const list = useRef<HTMLDivElement>(null)
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [typed, setTyped] = useState(false)
  const [active, setActive] = useState(-1)
  const selected = options.find((option) => option.id === value)
  const label = selected ? `${selected.parent ? selected.parent + ' / ' : ''}${selected.name}` : ''
  const results = options.filter((option) => `${option.parent || ''} ${option.name}`.toLowerCase().includes(typed ? query.toLowerCase() : ''))
  const show = () => { if (!open) { setOpen(true); setQuery(label); setTyped(false); setActive(-1) } }
  const choose = (index: number) => {
    const option = results[index]
    if (!option || option.included) return
    onChange(option.id); setOpen(false); setActive(-1)
  }
  useEffect(() => { if (open && active >= 0) list.current?.querySelector(`[data-index="${active}"]`)?.scrollIntoView({ block: 'nearest' }) }, [open, active])
  const key = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'Escape' && open) { event.preventDefault(); event.stopPropagation(); setOpen(false); return }
    if (event.key === 'Tab') { setOpen(false); return }
    if (event.key === 'Enter' && open) { event.preventDefault(); choose(active); return }
    if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return
    event.preventDefault(); show()
    const step = event.key === 'ArrowDown' ? 1 : -1
    let next = active < 0 ? (step === 1 ? -1 : 0) : active
    for (let i = 0; i < results.length; i++) { next = (next + step + results.length) % results.length; if (!results[next].included) { setActive(next); break } }
  }
  return <div className="min-w-0" onBlur={(event) => { if (!event.currentTarget.contains(event.relatedTarget)) setOpen(false) }}>
    <label htmlFor={`${id}-input`} className="mb-1 block text-xs font-medium">Environment</label>
    <div className="relative">
      <Input ref={input} id={`${id}-input`} className="h-8 pr-8 text-xs" role="combobox" aria-autocomplete="list" aria-controls={`${id}-options`} aria-expanded={open} aria-activedescendant={open && active >= 0 ? `${id}-${active}` : undefined} autoComplete="off" spellCheck={false} value={open ? query : label} placeholder={loading ? 'Loading…' : 'Select environment…'} disabled={disabled || loading} onFocus={(event) => { show(); event.currentTarget.select() }} onClick={show} onChange={(event) => { setQuery(event.target.value); setTyped(true); setActive(-1); setOpen(true); onChange('') }} onKeyDown={key} />
      <button type="button" tabIndex={-1} className="absolute inset-y-0 right-0 flex w-8 items-center justify-center text-muted-foreground" aria-label="Choose environment" disabled={disabled || loading} onMouseDown={(event) => event.preventDefault()} onClick={() => { if (open) setOpen(false); else { input.current?.focus(); show() } }}><ChevronDown className="size-3.5" /></button>
    </div>
    {open && <div ref={list} id={`${id}-options`} role="listbox" aria-label="Environments and sessions" className="mt-1 max-h-44 overflow-y-auto rounded-md border bg-popover p-1 text-popover-foreground">
      {results.map((option, index) => <div key={option.id} id={`${id}-${index}`} data-index={index} role="option" aria-selected={option.id === value} aria-disabled={option.included} className={`flex items-start justify-between gap-2 rounded-sm px-2 py-1.5 text-xs ${option.included ? 'cursor-default opacity-40' : 'cursor-pointer hover:bg-accent'} ${index === active ? 'bg-accent' : ''}`} onMouseDown={(event) => event.preventDefault()} onMouseMove={() => { if (!option.included) setActive(index) }} onClick={() => choose(index)}><span className="min-w-0 break-words">{option.name}{option.parent && <span className="block text-[10px] text-muted-foreground">{option.parent} / session</span>}</span><span className="shrink-0 text-[10px] text-muted-foreground">{option.included ? 'Included' : option.id === value ? <Check className="size-3" /> : `${option.hosts} ${option.hosts === 1 ? 'URL' : 'URLs'}`}</span></div>)}
      {!results.length && <p className="px-2 py-2 text-xs text-muted-foreground">No environments found.</p>}
    </div>}
  </div>
}
