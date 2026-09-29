import type {MetroTrain} from './api';

// Estimated operational direction is explicitly distinct from physical confirmation.
export function metroCurrentDirection(train:MetroTrain|undefined,now:number):boolean{
 return !!train&&train.association==='supported'&&Date.parse(train.valid_until)>now&&['estimated','confirmed'].includes(train.direction_evidence?.state??'');
}
