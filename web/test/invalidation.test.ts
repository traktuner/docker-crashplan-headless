import assert from 'node:assert/strict'
import test from 'node:test'
import { QueryClient, QueryObserver } from '@tanstack/react-query'
import { refreshStatus } from '../src/shared/invalidation.ts'

test('status events preserve slow browsing and deduplicate pending status reads', async () => {
 const client = new QueryClient({defaultOptions:{queries:{retry:false}}})
 let browsing=0, status=0
 const resolvers: (()=>void)[]=[]
 const browse = new QueryObserver(client,{queryKey:['archive',{}],queryFn:async()=>{browsing++; return ['cached']}})
 const state = new QueryObserver(client,{queryKey:['state'],queryFn:async()=>{status++; if(status>1) await new Promise<void>(r=>resolvers.push(r));return {}}})
 const off=[browse.subscribe(()=>{}),state.subscribe(()=>{})]
 try {
 await new Promise(r=>setTimeout(r,0))
 const pending=[refreshStatus(client),refreshStatus(client),refreshStatus(client)]
 await new Promise(r=>setTimeout(r,0))
 assert.equal(browsing,1)
 assert.equal(status,2)
 resolvers.forEach(r=>r());await Promise.all(pending)
 } finally {resolvers.forEach(r=>r());off.forEach(f=>f());client.clear()}
})

test('status events do not restart failed status reads', async () => {
 const client = new QueryClient({defaultOptions:{queries:{retry:false}}})
 let calls=0
 const failing = new QueryObserver(client,{queryKey:['backup-summary'],queryFn:async()=>{calls++; throw new Error('NATIVE_AUTHORIZATION_REQUIRED')}})
 const off=failing.subscribe(()=>{})
 try {
 await new Promise(r=>setTimeout(r,0))
 assert.equal(calls,1)
 await refreshStatus(client)
 await new Promise(r=>setTimeout(r,0))
 assert.equal(calls,1)
 assert.equal(failing.getCurrentResult().status,'error')
 } finally {off();client.clear()}
})
