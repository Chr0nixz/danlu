import { Link } from 'react-router'
import { buttonClass, Card, Empty } from '../components/ui'

export function NotFound() {
  return (
    <Card>
      <Empty action={<Link to="/" className={buttonClass('secondary', 'sm')}>回到总览</Link>}>没有这个页面，地址可能输错了</Empty>
    </Card>
  )
}
