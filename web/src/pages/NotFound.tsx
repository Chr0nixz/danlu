import { Link } from 'react-router'
import { buttonClass, Card, Empty } from '../components/ui'
import { t } from '../lib/i18n'

export function NotFound() {
  return (
    <Card>
      <Empty action={<Link to="/" className={buttonClass('secondary', 'sm')}>{t('ui.back_home')}</Link>}>{t('notfound.message')}</Empty>
    </Card>
  )
}
