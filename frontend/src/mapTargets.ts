export type MapTarget={id:string,kind:'vehicle'|'stop',x:number,y:number};
const cellSize=48;
const key=(x:number,y:number)=>`${Math.floor(x/cellSize)}:${Math.floor(y/cellSize)}`;
// Presentation-only screen coordinates. Original GeoJSON observations stay untouched.
export function separateMapTargets(vehicles:MapTarget[],stops:MapTarget[]){
 const occupied=new Map<string,MapTarget[]>(),offsets=new Map<string,[number,number]>(),slots=new Map<string,number>();
 const insert=(t:MapTarget)=>{const k=key(t.x,t.y),bucket=occupied.get(k);if(bucket)bucket.push(t);else occupied.set(k,[t])};
 const neighbors=(x:number,y:number)=>{const out:MapTarget[]=[];for(let dx=-1;dx<=1;dx++)for(let dy=-1;dy<=1;dy++)out.push(...occupied.get(key(x+dx*cellSize,y+dy*cellSize))??[]);return out};
 for(const stop of stops)insert(stop);
 for(const vehicle of [...vehicles].sort((a,b)=>a.id.localeCompare(b.id))){
  const dense=Array.from({length:9},(_,i)=>(occupied.get(key(vehicle.x+(i%3-1)*cellSize,vehicle.y+(Math.floor(i/3)-1)*cellSize))?.length??0)>64).some(Boolean);
  const station=(dense?[]:neighbors(vehicle.x,vehicle.y)).filter(t=>t.kind==='stop'&&Math.hypot(t.x-vehicle.x,t.y-vehicle.y)<48).sort((a,b)=>Math.hypot(a.x-vehicle.x,a.y-vehicle.y)-Math.hypot(b.x-vehicle.x,b.y-vehicle.y)||a.id.localeCompare(b.id))[0];
  let target=vehicle;
  if(station&&(slots.get(station.id)??0)<8){
   for(let slot=0;slot<8;slot++){
    const angle=(-45+slot*45)*Math.PI/180,x=station.x+72*Math.cos(angle),y=station.y+72*Math.sin(angle);
    if(neighbors(x,y).some(t=>Math.hypot(t.x-x,t.y-y)<48))continue;
    target={...vehicle,x,y};offsets.set(vehicle.id,[x-vehicle.x,y-vehicle.y]);slots.set(station.id,(slots.get(station.id)??0)+1);break;
   }
  }
  insert(target);
 }
 return {offsets,hit:(x:number,y:number)=>neighbors(x,y).filter(t=>Math.abs(t.x-x)<=22&&Math.abs(t.y-y)<=22)};
}
