import type {Stop,Vehicle} from './api';
import {canonicalStop,canonicalStops} from './stationIdentity';
export type MapTarget={kind:'stop',stop:Stop}|{kind:'vehicle',vehicle:Vehicle};
export function nearbyStops(anchor:Stop,stops:Stop[]):Stop[]{
 anchor=canonicalStop(anchor,stops);
 const radians=Math.PI/180;
 const distance=(s:Stop)=>{const lat=(s.lat-anchor.lat)*radians,lon=(s.lon-anchor.lon)*radians;const h=Math.sin(lat/2)**2+Math.cos(anchor.lat*radians)*Math.cos(s.lat*radians)*Math.sin(lon/2)**2;return 6371000*2*Math.asin(Math.min(1,Math.sqrt(h)))};
 const unique=new Map(canonicalStops(stops).map(s=>[s.id,s]));unique.set(anchor.id,anchor);
 return [anchor,...[...unique.values()].filter(s=>s.id!==anchor.id&&distance(s)<=50+1e-6).sort((a,b)=>distance(a)-distance(b)||a.id.localeCompare(b.id))];
}
