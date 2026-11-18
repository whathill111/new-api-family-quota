import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { CopyButton } from '@/components/copy-button'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { api } from '@/lib/api'
import { formatQuotaWithCurrency } from '@/lib/currency'
import { requireServerSuccess } from '@/lib/server-error-message'

type FamilyQuotaState = {
  group: string
  member: boolean
  can_manage: boolean
  balance: number
}

const base = '/api/family-quota'

export function FamilyQuotaCard(props: { userId: number }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const queryKey = ['family-quota', props.userId]
  const [code, setCode] = useState('')
  const state = useQuery({
    queryKey,
    queryFn: async () => {
      const response = await api.get(base)
      return requireServerSuccess(response.data).data as FamilyQuotaState
    },
  })
  const redeem = useMutation({
    mutationFn: async (value: string) => {
      const response = await api.post(`${base}/redeem`, { code: value })
      requireServerSuccess(response.data)
    },
    onSuccess: async () => {
      setCode('')
      toast.success(t('Family activity quota redeemed.'))
      await queryClient.invalidateQueries({ queryKey })
    },
  })

  if (!state.data?.member && !state.data?.can_manage) return null

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (code.trim() && !redeem.isPending) redeem.mutate(code.trim())
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('Family activity quota')}</CardTitle>
        <CardDescription>{state.data.group}</CardDescription>
      </CardHeader>
      <CardContent className='flex flex-col gap-6'>
        {state.data.member && (
          <form onSubmit={submit}>
            <FieldGroup>
              <Field>
                <FieldLabel htmlFor='family-quota-code'>
                  {t('Family activity code')}
                </FieldLabel>
                <FieldDescription>
                  {t('Family activity balance')}{' '}
                  <strong>{formatQuotaWithCurrency(state.data.balance)}</strong>
                </FieldDescription>
                <Input
                  id='family-quota-code'
                  autoComplete='off'
                  spellCheck={false}
                  maxLength={64}
                  value={code}
                  onChange={(e) => setCode(e.target.value)}
                  disabled={redeem.isPending}
                  required
                />
              </Field>
              <div>
                <Button
                  type='submit'
                  disabled={!code.trim() || redeem.isPending}
                >
                  {t('Redeem family quota')}
                </Button>
              </div>
            </FieldGroup>
          </form>
        )}
        {state.data.can_manage && <FamilyQuotaAdmin />}
      </CardContent>
    </Card>
  )
}

function FamilyQuotaAdmin() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [userId, setUserId] = useState('')
  const [amount, setAmount] = useState('')
  const [count, setCount] = useState('1')
  const [days, setDays] = useState('30')
  const [codes, setCodes] = useState<string[]>([])
  const grant = useMutation({
    mutationFn: async () => {
      const response = await api.post(`${base}/admin/grant`, {
        user_id: Number(userId),
        amount_usd: amount.trim(),
      })
      requireServerSuccess(response.data)
    },
    onSuccess: async () => {
      toast.success(t('Family activity quota added.'))
      await queryClient.invalidateQueries({ queryKey: ['family-quota'] })
    },
  })
  const issue = useMutation({
    mutationFn: async () => {
      const response = await api.post(`${base}/admin/codes`, {
        user_id: Number(userId),
        amount_usd: amount.trim(),
        count: Number(count),
        valid_days: Number(days),
      })
      const data = requireServerSuccess(response.data).data as {
        codes: string[]
      }
      return data.codes
    },
    onSuccess: (issued) => setCodes(issued),
  })

  const pending = grant.isPending || issue.isPending
  const validAmount = /^\d+(?:\.\d{1,6})?$/.test(amount) && Number(amount) > 0
  const validTarget =
    /^[1-9]\d*$/.test(userId) && Number.isSafeInteger(Number(userId))
  const validIssue =
    validAmount &&
    (userId === '' || (validTarget && count === '1')) &&
    Number.isInteger(Number(count)) &&
    Number(count) >= 1 &&
    Number(count) <= 100 &&
    Number.isInteger(Number(days)) &&
    Number(days) >= 1 &&
    Number(days) <= 365

  return (
    <div className='flex flex-col gap-4'>
      <h3 className='font-medium'>{t('Manage family activity quota')}</h3>
      <FieldGroup className='grid sm:grid-cols-2'>
        <Field>
          <FieldLabel htmlFor='family-target'>{t('User ID')}</FieldLabel>
          <Input
            id='family-target'
            inputMode='numeric'
            maxLength={15}
            value={userId}
            onChange={(e) => setUserId(e.target.value)}
            disabled={pending}
          />
          <FieldDescription>
            {t('Leave blank to issue codes for any family member.')}
          </FieldDescription>
        </Field>
        <Field>
          <FieldLabel htmlFor='family-amount'>
            {t('Quota amount (USD)')}
          </FieldLabel>
          <Input
            id='family-amount'
            inputMode='decimal'
            maxLength={32}
            value={amount}
            onChange={(e) => setAmount(e.target.value)}
            disabled={pending}
          />
        </Field>
        <Field>
          <FieldLabel htmlFor='family-count'>{t('Code count')}</FieldLabel>
          <Input
            id='family-count'
            inputMode='numeric'
            maxLength={3}
            value={count}
            onChange={(e) => setCount(e.target.value)}
            disabled={pending}
          />
        </Field>
        <Field>
          <FieldLabel htmlFor='family-days'>
            {t('Code validity (days)')}
          </FieldLabel>
          <Input
            id='family-days'
            inputMode='numeric'
            maxLength={3}
            value={days}
            onChange={(e) => setDays(e.target.value)}
            disabled={pending}
          />
        </Field>
      </FieldGroup>
      <div className='flex flex-wrap gap-2'>
        <Button
          type='button'
          disabled={pending || !validAmount || !validTarget}
          onClick={() => grant.mutate()}
        >
          {t('Add family quota')}
        </Button>
        <Button
          type='button'
          variant='outline'
          disabled={pending || !validIssue}
          onClick={() => issue.mutate()}
        >
          {t('Generate family codes')}
        </Button>
      </div>
      {codes.length > 0 && (
        <Field>
          <FieldLabel htmlFor='family-issued'>
            {t('New family codes')}
          </FieldLabel>
          <FieldDescription>
            {t('Copy these codes now. They are shown only once.')}
          </FieldDescription>
          <Textarea
            id='family-issued'
            readOnly
            rows={4}
            spellCheck={false}
            value={codes.join('\n')}
          />
          <div className='flex flex-wrap gap-2'>
            <CopyButton size='sm' variant='outline' value={codes.join('\n')}>
              {t('Copy to clipboard')}
            </CopyButton>
            <Button
              type='button'
              size='sm'
              variant='ghost'
              onClick={() => setCodes([])}
            >
              {t('Hide family codes')}
            </Button>
          </div>
        </Field>
      )}
    </div>
  )
}
