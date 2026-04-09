import { useState } from 'react'
import { changeMyPassword } from '../api'
import { Card, CardBody, CardHeader, Input, Button, PageHeader, ErrorState, SuccessState } from '../components/ui'

export function AccountPage() {
  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [success, setSuccess] = useState<string | null>(null)

  const onSubmit = async () => {
    setError(null)
    setSuccess(null)

    if (currentPassword.trim() === '') {
      setError('Current password is required')
      return
    }
    if (newPassword.length < 8) {
      setError('New password must be at least 8 characters')
      return
    }
    if (newPassword !== confirmPassword) {
      setError('New password and confirmation do not match')
      return
    }

    setSaving(true)
    try {
      await changeMyPassword(currentPassword, newPassword)
      setCurrentPassword('')
      setNewPassword('')
      setConfirmPassword('')
      setSuccess('Password updated successfully')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'failed to change password')
    } finally {
      setSaving(false)
    }
  }

  return (
    <>
      <PageHeader title="Account Security" />
      <div className="max-w-2xl space-y-4">
        {error && <ErrorState message={error} />}
        {success && <SuccessState message={success} />}
        <Card>
          <CardHeader title="Change Password" />
          <CardBody>
            <div className="space-y-4">
              <Input
                label="Current Password"
                type="password"
                value={currentPassword}
                onChange={setCurrentPassword}
                placeholder="Enter current password"
              />
              <Input
                label="New Password"
                type="password"
                value={newPassword}
                onChange={setNewPassword}
                placeholder="At least 8 characters"
              />
              <Input
                label="Confirm New Password"
                type="password"
                value={confirmPassword}
                onChange={setConfirmPassword}
                placeholder="Re-enter new password"
              />
              <div className="flex justify-end">
                <Button disabled={saving} onClick={() => void onSubmit()}>
                  {saving ? 'Updating...' : 'Change Password'}
                </Button>
              </div>
            </div>
          </CardBody>
        </Card>
      </div>
    </>
  )
}
