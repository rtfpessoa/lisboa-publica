import {metroCurrentDirection} from './metroEvidence';
import {useEffect,useMemo,useState} from 'react';
import type {MetroLiveFrame,Vehicle} from './api';

// Rendering uses absolute source-bound intervals. It emits no samples/events,
// renews no observation clock and performs no network requests.
export function metroModelVehicles(frame:MetroLiveFrame|undefined,now:number):Vehicle[]{
 if(!frame)return [];
 const linked=new Map(frame.trains.filter(t=>metroCurrentDirection(t,now)&&t.vehicle_id).map(t=>[t.vehicle_id,t]));
 return frame.vehicles.map(vehicle=>{
  const train=linked.get(vehicle.id),p=train?.model_projection;
  if(!p||train?.lifecycle?.state!=='active'||vehicle.last_known||vehicle.stale)return vehicle;
  const source=Date.parse(p.source_updated_at),from=Date.parse(p.from_at),to=Date.parse(p.to_at),expiry=Date.parse(p.valid_until);
  if(!p.geometry_version||!p.model_version||![source,from,to,expiry,p.from_lat,p.from_lon,p.to_lat,p.to_lon].every(Number.isFinite)||source>now||expiry<=now||to<=from||now<from||now>=to)return vehicle;
  const progress=(now-from)/(to-from);
  return {...vehicle,lat:p.from_lat+(p.to_lat-p.from_lat)*progress,lon:p.from_lon+(p.to_lon-p.from_lon)*progress,position_kind:'estimated'};
 });
}
const emptyVehicles:Vehicle[]=[];
export function useMetroModelVehicles(frame:MetroLiveFrame|undefined){
 const [now,setNow]=useState(Date.now());
 const signature=JSON.stringify([frame?.vehicles,frame?.trains.map(t=>[t.vehicle_id,t.association,t.valid_until,t.lifecycle?.state,t.direction_evidence,t.model_projection])]);
 const stable=useMemo(()=>frame,[signature]);
 const modeled=!!stable?.trains.some(t=>t.model_projection&&metroCurrentDirection(t,Date.now()));
 useEffect(()=>{if(!modeled)return;const timer=setInterval(()=>{if(!document.hidden)setNow(Date.now())},500);const visible=()=>setNow(Date.now());visible();document.addEventListener('visibilitychange',visible);return()=>{clearInterval(timer);document.removeEventListener('visibilitychange',visible)}},[modeled]);
 return useMemo(()=>modeled?metroModelVehicles(stable,now):stable?.vehicles??emptyVehicles,[stable,modeled,now]);
}
